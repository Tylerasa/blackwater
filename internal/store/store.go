// Package store persists normalised transactions in a local SQLite file.
//
// One DB file per user. Idempotency is enforced by hashing (sender, body)
// into message_hash and using INSERT OR IGNORE — running ingest twice on
// the same dump is a no-op, not a source of duplicates.
//
// modernc.org/sqlite (pure Go) is used deliberately: no CGO means
// cross-compiling for Android in milestone 5 stays trivial.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Tylerasa/blackwater/internal/normalize"
)

//go:embed schema.sql
var schemaSQL string

// currentSchemaVersion is bumped whenever schema.sql changes in a way that
// existing DBs need to migrate. Since we use CREATE TABLE IF NOT EXISTS,
// version 1 == "the schema currently in schema.sql". Additive changes go
// in numbered migrations added below; destructive changes need a real
// migration story we don't yet have.
const currentSchemaVersion = 1

// Store owns the *sql.DB and the transactional helpers around it.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite file and runs any pending migrations.
// Pass ":memory:" for a throwaway in-memory DB (used by tests).
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		// Foreign keys on, WAL for durability + concurrent readers.
		dsn = path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// SQLite handles concurrency by serialising writes; a single
	// connection avoids surprises with in-memory DBs where each conn
	// gets its own database.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying DB.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying *sql.DB for callers that need to run ad-hoc
// queries. Prefer the typed methods below.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("store: apply schema: %w", err)
	}
	// Record which schema version this DB is at. INSERT OR IGNORE so
	// re-opens are idempotent.
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, currentSchemaVersion); err != nil {
		return fmt.Errorf("store: record schema version: %w", err)
	}
	return nil
}

// MessageHash is the idempotency key. Two ingests of the same (sender,
// body) always produce the same hash.
func MessageHash(sender, body string) string {
	h := sha256.New()
	h.Write([]byte(sender))
	h.Write([]byte{0})
	h.Write([]byte(body))
	return hex.EncodeToString(h.Sum(nil))
}

// InsertResult reports whether Insert actually persisted a new row.
type InsertResult struct {
	Inserted bool
	ID       int64 // only valid if Inserted
}

// Insert persists tx into the store keyed by messageHash. If a row with
// the same messageHash already exists, Insert returns Inserted=false and
// no error.
func (s *Store) Insert(ctx context.Context, messageHash string, tx normalize.Transaction) (InsertResult, error) {
	if messageHash == "" {
		return InsertResult{}, errors.New("store: messageHash required")
	}
	rawJSON, err := marshalRawFields(tx.RawFields)
	if err != nil {
		return InsertResult{}, err
	}
	var timestamp any
	if !tx.Timestamp.IsZero() {
		timestamp = tx.Timestamp.UTC().Format(time.RFC3339)
	}
	res, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO transactions (
    message_hash, fingerprint, sender, timestamp, direction,
    amount_pesewas, currency, counterparty, reference,
    balance_pesewas, fee_pesewas, tax_pesewas, raw_fields
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		messageHash, tx.Fingerprint, tx.Sender, timestamp, string(tx.Direction),
		tx.Amount, tx.Currency, nullIfEmpty(tx.Counterparty), nullIfEmpty(tx.Reference),
		tx.Balance, tx.Fee, tx.Tax, rawJSON,
	)
	if err != nil {
		return InsertResult{}, fmt.Errorf("store: insert: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return InsertResult{}, err
	}
	if rows == 0 {
		return InsertResult{Inserted: false}, nil
	}
	id, err := res.LastInsertId()
	if err != nil {
		return InsertResult{}, err
	}
	return InsertResult{Inserted: true, ID: id}, nil
}

// DirectionTotal is one row of the sum-by-direction aggregate.
type DirectionTotal struct {
	Direction     string
	Count         int
	TotalPesewas  int64
}

// SumByDirection returns totals grouped by direction, optionally bounded
// by a time window. Pass zero-value times to skip a bound.
func (s *Store) SumByDirection(ctx context.Context, since, until time.Time) ([]DirectionTotal, error) {
	query := `SELECT direction, COUNT(*), COALESCE(SUM(amount_pesewas), 0)
	          FROM transactions WHERE 1=1`
	args := []any{}
	if !since.IsZero() {
		query += ` AND timestamp >= ?`
		args = append(args, since.UTC().Format(time.RFC3339))
	}
	if !until.IsZero() {
		query += ` AND timestamp <= ?`
		args = append(args, until.UTC().Format(time.RFC3339))
	}
	query += ` GROUP BY direction ORDER BY direction`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: sum: %w", err)
	}
	defer rows.Close()
	var out []DirectionTotal
	for rows.Next() {
		var d DirectionTotal
		if err := rows.Scan(&d.Direction, &d.Count, &d.TotalPesewas); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Count returns the total number of stored transactions. Handy for CLI
// summaries and tests.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transactions`).Scan(&n)
	return n, err
}

func marshalRawFields(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("store: marshal raw fields: %w", err)
	}
	return string(b), nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
