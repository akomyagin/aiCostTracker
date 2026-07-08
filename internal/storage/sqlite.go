package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/provider"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO), registers "sqlite"
)

// dayLayout is how a UsageRecord.Day (UTC midnight) is stored as TEXT, giving a
// stable, sortable primary-key component and human-readable rows.
const dayLayout = "2006-01-02"

// sqliteStore is the SQLite-backed Store.
type sqliteStore struct {
	db *sql.DB
}

var _ Store = (*sqliteStore)(nil)

// Open opens (creating if needed) the SQLite-backed Store at path and applies
// the schema. The path's parent directory must already exist.
func Open(path string) (Store, error) {
	// _pragma busy_timeout guards against transient "database is locked" on a
	// concurrent run; foreign_keys is harmless-forward-looking.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// A single writer is plenty for a CLI and avoids WAL contention surprises.
	db.SetMaxOpenConns(1)

	s := &sqliteStore{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// migrate creates the schema if absent. The snapshots table keys on
// (provider, day, model) so Save can upsert idempotently.
func (s *sqliteStore) migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS usage_records (
    provider      TEXT    NOT NULL,
    day           TEXT    NOT NULL, -- YYYY-MM-DD, UTC
    model         TEXT    NOT NULL, -- "" when the provider does not break out models
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL    NOT NULL DEFAULT 0,
    fetched_at    TEXT    NOT NULL, -- RFC3339 of the snapshot that last wrote this row
    PRIMARY KEY (provider, day, model)
);`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// Save writes every record of the snapshot via an idempotent upsert inside one
// transaction. Re-saving the same (provider, day, model) overwrites the row with
// the fresh values rather than accumulating duplicates.
func (s *sqliteStore) Save(ctx context.Context, snap provider.Snapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	const upsert = `
INSERT INTO usage_records
    (provider, day, model, input_tokens, output_tokens, cost_usd, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, day, model) DO UPDATE SET
    input_tokens  = excluded.input_tokens,
    output_tokens = excluded.output_tokens,
    cost_usd      = excluded.cost_usd,
    fetched_at    = excluded.fetched_at;`

	stmt, err := tx.PrepareContext(ctx, upsert)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	fetchedAt := snap.FetchedAt.UTC().Format(time.RFC3339)
	for _, r := range snap.Records {
		if _, err := stmt.ExecContext(ctx,
			r.Provider,
			r.Day.UTC().Format(dayLayout),
			r.Model,
			r.InputTokens,
			r.OutputTokens,
			r.CostUSD,
			fetchedAt,
		); err != nil {
			return fmt.Errorf("upsert record (%s %s %s): %w", r.Provider, r.Day.Format(dayLayout), r.Model, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Query returns stored records for the window [Start, End), optionally filtered
// to one provider. Days are compared as TEXT, which sorts correctly for the
// YYYY-MM-DD layout. Results are ordered (day, provider, model) for determinism.
func (s *sqliteStore) Query(ctx context.Context, providerID string, w provider.Window) ([]provider.UsageRecord, error) {
	start := w.Start.UTC().Format(dayLayout)
	end := w.End.UTC().Format(dayLayout)

	query := `
SELECT provider, day, model, input_tokens, output_tokens, cost_usd
FROM usage_records
WHERE day >= ? AND day < ?`
	args := []any{start, end}
	if providerID != "" {
		query += " AND provider = ?"
		args = append(args, providerID)
	}
	query += " ORDER BY day, provider, model;"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query usage_records: %w", err)
	}
	defer rows.Close()

	var out []provider.UsageRecord
	for rows.Next() {
		var (
			rec    provider.UsageRecord
			dayStr string
		)
		if err := rows.Scan(&rec.Provider, &dayStr, &rec.Model, &rec.InputTokens, &rec.OutputTokens, &rec.CostUSD); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		day, err := time.Parse(dayLayout, dayStr)
		if err != nil {
			return nil, fmt.Errorf("parse stored day %q: %w", dayStr, err)
		}
		rec.Day = day.UTC()
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return out, nil
}

// Close releases the database handle.
func (s *sqliteStore) Close() error {
	return s.db.Close()
}
