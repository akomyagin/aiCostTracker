package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
// the schema. The parent directory is created if it doesn't exist yet (a fresh
// system has no ~/.config/aicost/ until something creates it).
func Open(path string) (Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory for %s: %w", path, err)
	}

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

// usageRecordsDDL is the current schema. cost_micros is INTEGER on purpose:
// a REAL column would coerce stored int64 micro-USD back to REAL (SQLite type
// affinity) and lose precision above 2^53, defeating the whole migration.
const usageRecordsDDL = `
CREATE TABLE IF NOT EXISTS usage_records (
    provider      TEXT    NOT NULL,
    day           TEXT    NOT NULL, -- YYYY-MM-DD, UTC
    model         TEXT    NOT NULL, -- "" when the provider does not break out models
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_micros   INTEGER NOT NULL DEFAULT 0, -- integer micro-USD, 1 USD = 1e6
    fetched_at    TEXT    NOT NULL, -- RFC3339 of the snapshot that last wrote this row
    PRIMARY KEY (provider, day, model)
);`

// migrate brings the schema to the current version. It first rebuilds any
// pre-Этап-9 table that still stores cost as REAL cost_usd into the INTEGER
// cost_micros schema (preserving the user's history), then creates the table
// if it does not exist at all. The snapshots table keys on (provider, day,
// model) so Save can upsert idempotently.
func (s *sqliteStore) migrate(ctx context.Context) error {
	if err := s.migrateLegacyCostColumn(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, usageRecordsDDL); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// migrateLegacyCostColumn detects the old `cost_usd REAL` column and, if found,
// rebuilds the table with the new `cost_micros INTEGER` column inside a single
// transaction, converting each value via ROUND(cost_usd * 1e6) (SQLite ROUND is
// half away from zero, matching provider.DollarsToMicros). It is a no-op on a
// fresh DB (no table yet) and on an already-migrated DB (cost_micros present),
// so re-opening the same file is idempotent. SetMaxOpenConns(1) guarantees no
// concurrent reader observes the table mid-rebuild.
func (s *sqliteStore) migrateLegacyCostColumn(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(usage_records)`)
	if err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	var hasLegacyCost, hasMicros bool
	func() {
		defer rows.Close()
		for rows.Next() {
			var (
				cid     int
				name    string
				typ     string
				notnull int
				dflt    sql.NullString
				pk      int
			)
			if scanErr := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); scanErr != nil {
				err = fmt.Errorf("scan column info: %w", scanErr)
				return
			}
			switch name {
			case "cost_usd":
				hasLegacyCost = true
			case "cost_micros":
				hasMicros = true
			}
		}
		if rowsErr := rows.Err(); rowsErr != nil && err == nil {
			err = fmt.Errorf("iterate column info: %w", rowsErr)
		}
	}()
	if err != nil {
		return err
	}
	// No table (fresh DB) or already migrated: nothing to convert.
	if !hasLegacyCost || hasMicros {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	const rebuild = `
CREATE TABLE usage_records_new (
    provider      TEXT    NOT NULL,
    day           TEXT    NOT NULL,
    model         TEXT    NOT NULL,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_micros   INTEGER NOT NULL DEFAULT 0,
    fetched_at    TEXT    NOT NULL,
    PRIMARY KEY (provider, day, model)
);
INSERT INTO usage_records_new
    (provider, day, model, input_tokens, output_tokens, cost_micros, fetched_at)
SELECT provider, day, model, input_tokens, output_tokens,
       CAST(ROUND(cost_usd * 1000000) AS INTEGER),
       fetched_at
FROM usage_records;
DROP TABLE usage_records;
ALTER TABLE usage_records_new RENAME TO usage_records;`
	if _, err := tx.ExecContext(ctx, rebuild); err != nil {
		return fmt.Errorf("migrate cost_usd -> cost_micros: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
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
    (provider, day, model, input_tokens, output_tokens, cost_micros, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, day, model) DO UPDATE SET
    input_tokens  = excluded.input_tokens,
    output_tokens = excluded.output_tokens,
    cost_micros   = excluded.cost_micros,
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
			r.CostMicros,
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
SELECT provider, day, model, input_tokens, output_tokens, cost_micros
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
		if err := rows.Scan(&rec.Provider, &dayStr, &rec.Model, &rec.InputTokens, &rec.OutputTokens, &rec.CostMicros); err != nil {
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
