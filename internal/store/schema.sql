-- Schema for the local transaction store. One file lives per user.
-- Money is always stored as INTEGER pesewas so downstream aggregates are
-- exact. Timestamps are TEXT in ISO-8601 UTC ("2006-01-02T15:04:05Z") so
-- ORDER BY and BETWEEN work correctly and SQLite datetime() understands
-- them.

CREATE TABLE IF NOT EXISTS transactions (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    -- sha256(sender || \x00 || body). Deduplicates re-ingested dumps.
    message_hash      TEXT NOT NULL UNIQUE,
    -- The template fingerprint the spec came from — traceability.
    fingerprint       TEXT NOT NULL,
    sender            TEXT NOT NULL,
    -- Zero-value time.Time is stored as NULL, not "0001-01-01T...".
    timestamp         TEXT,
    direction         TEXT NOT NULL,
    amount_pesewas    INTEGER NOT NULL DEFAULT 0,
    currency          TEXT NOT NULL DEFAULT 'GHS',
    counterparty      TEXT,
    reference         TEXT,
    balance_pesewas   INTEGER NOT NULL DEFAULT 0,
    fee_pesewas       INTEGER NOT NULL DEFAULT 0,
    tax_pesewas       INTEGER NOT NULL DEFAULT 0,
    -- Full spec.Execute output as JSON, for debugging or re-normalising
    -- if the canonical shape ever changes.
    raw_fields        TEXT,
    imported_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_txn_timestamp    ON transactions(timestamp);
CREATE INDEX IF NOT EXISTS idx_txn_direction    ON transactions(direction);
CREATE INDEX IF NOT EXISTS idx_txn_counterparty ON transactions(counterparty);
CREATE INDEX IF NOT EXISTS idx_txn_fingerprint  ON transactions(fingerprint);

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
