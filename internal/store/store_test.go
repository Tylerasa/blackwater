package store

import (
	"context"
	"testing"
	"time"

	"github.com/Tylerasa/blackwater/internal/normalize"
	"github.com/Tylerasa/blackwater/internal/spec"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMessageHashStable(t *testing.T) {
	h1 := MessageHash("MobileMoney", "hello world")
	h2 := MessageHash("MobileMoney", "hello world")
	h3 := MessageHash("MTN", "hello world")
	if h1 != h2 {
		t.Errorf("same input must hash equally")
	}
	if h1 == h3 {
		t.Errorf("different sender must hash differently")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char sha256 hex, got %d", len(h1))
	}
}

func TestOpenCreatesSchema(t *testing.T) {
	s := newStore(t)
	// A round-trip on schema_migrations proves migrate ran.
	var v int
	if err := s.db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != currentSchemaVersion {
		t.Errorf("schema version: got %d want %d", v, currentSchemaVersion)
	}
}

func TestInsertIdempotent(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tx := normalize.Transaction{
		Fingerprint: "fp1",
		Sender:      "MobileMoney",
		Direction:   spec.DirectionDebit,
		Amount:      300,
		Currency:    "GHS",
		Timestamp:   time.Date(2025, 2, 9, 9, 57, 48, 0, time.UTC),
		RawFields:   map[string]string{"amount": "3.00"},
	}
	hash := MessageHash("MobileMoney", "sample body 1")

	r1, err := s.Insert(ctx, hash, tx)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Inserted {
		t.Fatal("first insert should persist")
	}
	if r1.ID == 0 {
		t.Error("first insert should return a row ID")
	}

	// Same hash again — should not insert.
	r2, err := s.Insert(ctx, hash, tx)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Inserted {
		t.Fatal("duplicate insert should be skipped")
	}

	n, err := s.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count after dup insert: got %d want 1", n)
	}
}

func TestInsertRequiresHash(t *testing.T) {
	s := newStore(t)
	_, err := s.Insert(context.Background(), "", normalize.Transaction{Direction: spec.DirectionDebit})
	if err == nil {
		t.Fatal("expected error for empty message hash")
	}
}

func TestSumByDirection(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	seed := []struct {
		body   string
		dir    spec.Direction
		amount int64
		when   time.Time
	}{
		{"a", spec.DirectionDebit, 300, time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
		{"b", spec.DirectionDebit, 1000, time.Date(2025, 2, 15, 0, 0, 0, 0, time.UTC)},
		{"c", spec.DirectionCredit, 20000, time.Date(2025, 2, 20, 0, 0, 0, 0, time.UTC)},
		{"d", spec.DirectionDebit, 500, time.Date(2025, 3, 5, 0, 0, 0, 0, time.UTC)},
	}
	for _, x := range seed {
		_, err := s.Insert(ctx, MessageHash("MobileMoney", x.body), normalize.Transaction{
			Fingerprint: "fp", Sender: "MobileMoney",
			Direction: x.dir, Amount: x.amount, Currency: "GHS", Timestamp: x.when,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// no bounds — everything
	all, err := s.SumByDirection(ctx, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	byDir := map[string]DirectionTotal{}
	for _, d := range all {
		byDir[d.Direction] = d
	}
	if byDir["debit"].TotalPesewas != 1800 || byDir["debit"].Count != 3 {
		t.Errorf("unbounded debit: %+v", byDir["debit"])
	}
	if byDir["credit"].TotalPesewas != 20000 || byDir["credit"].Count != 1 {
		t.Errorf("unbounded credit: %+v", byDir["credit"])
	}

	// bounded to February only — drops the Mar 5 debit
	feb, err := s.SumByDirection(ctx,
		time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2025, 2, 28, 23, 59, 59, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	bd := map[string]DirectionTotal{}
	for _, d := range feb {
		bd[d.Direction] = d
	}
	if bd["debit"].TotalPesewas != 1300 || bd["debit"].Count != 2 {
		t.Errorf("Feb debit: got %+v, want total=1300 count=2", bd["debit"])
	}
}

func TestInsertHandlesNilRawFields(t *testing.T) {
	// Confirms marshalRawFields returns empty for a nil map without error.
	s := newStore(t)
	_, err := s.Insert(context.Background(), MessageHash("x", "y"), normalize.Transaction{
		Fingerprint: "fp", Sender: "x", Direction: spec.DirectionBalance,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInsertZeroTimestampStoresNull(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	hash := MessageHash("x", "y")
	if _, err := s.Insert(ctx, hash, normalize.Transaction{
		Fingerprint: "fp", Sender: "x", Direction: spec.DirectionDebit,
	}); err != nil {
		t.Fatal(err)
	}
	var ts *string
	if err := s.db.QueryRowContext(ctx, `SELECT timestamp FROM transactions WHERE message_hash=?`, hash).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	if ts != nil {
		t.Errorf("zero timestamp should store NULL, got %q", *ts)
	}
}
