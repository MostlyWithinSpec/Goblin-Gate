package logs

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store implements the SQLite log store.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Store.
func NewStore(dbPath string) (*Store, error) {
	// The events are traffic history: root only. SQLite gives its -wal and
	// -shm files the database file's mode, so create that one first.
	if dbPath != "" && !strings.HasPrefix(dbPath, ":memory:") && !strings.HasPrefix(dbPath, "file:") {
		if f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
			f.Close()
		}
	}
	// WAL lets the API read while the ingester writes; busy_timeout makes
	// writers wait for each other instead of failing.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Create tables
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			time INTEGER,
			kind TEXT,
			action TEXT,
			policy_id TEXT,
			src_ip TEXT,
			src_port INTEGER,
			dst_ip TEXT,
			dst_port INTEGER,
			proto TEXT,
			in_intf TEXT,
			out_intf TEXT,
			bytes INTEGER,
			app TEXT,
			category TEXT,
			signature TEXT,
			severity TEXT,
			message TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_events_time ON events(time);
		CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind);

		CREATE TABLE IF NOT EXISTS rollup_1m (
			time INTEGER,
			kind TEXT,
			action TEXT,
			policy_id TEXT,
			in_intf TEXT,
			out_intf TEXT,
			count INTEGER,
			bytes INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_rollup_1m_time ON rollup_1m(time);
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	return &Store{db: db}, nil
}

// Insert inserts an event into the store.
func (s *Store) Insert(event *Event) error {
	_, err := s.db.Exec(`
		INSERT INTO events (time, kind, action, policy_id, src_ip, src_port, dst_ip, dst_port, proto, in_intf, out_intf, bytes, app, category, signature, severity, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.Time, event.Kind, event.Action, event.PolicyID, event.SrcIP, event.SrcPort, event.DstIP, event.DstPort, event.Proto, event.InIntf, event.OutIntf, event.Bytes, event.App, event.Category, event.Signature, event.Severity, event.Message)
	if err != nil {
		return fmt.Errorf("failed to insert event: %w", err)
	}
	return nil
}

// InsertBatch stores events in one transaction.
func (s *Store) InsertBatch(events []*Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to insert events: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO events (time, kind, action, policy_id, src_ip, src_port, dst_ip, dst_port, proto, in_intf, out_intf, bytes, app, category, signature, severity, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("failed to insert events: %w", err)
	}
	defer stmt.Close()
	for _, e := range events {
		if _, err := stmt.Exec(e.Time, e.Kind, e.Action, e.PolicyID, e.SrcIP, e.SrcPort, e.DstIP, e.DstPort, e.Proto, e.InIntf, e.OutIntf, e.Bytes, e.App, e.Category, e.Signature, e.Severity, e.Message); err != nil {
			return fmt.Errorf("failed to insert events: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to insert events: %w", err)
	}
	return nil
}

// Rollup aggregates the last complete minute into rollup_1m (see RollupAt).
func (s *Store) Rollup() error {
	now := time.Now().Unix()
	return s.RollupAt(now - now%60 - 60)
}

// RollupAt (re)computes the rollup_1m rows of the minute starting at
// minute (Unix seconds, a multiple of 60). Running it twice for the same
// minute replaces the rows instead of counting events twice.
func (s *Store) RollupAt(minute int64) error {
	minute -= minute % 60
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to rollup events: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM rollup_1m WHERE time = ?`, minute); err != nil {
		return fmt.Errorf("failed to rollup events: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO rollup_1m (time, kind, action, policy_id, in_intf, out_intf, count, bytes)
		SELECT ?, kind, action, policy_id, in_intf, out_intf, COUNT(*), COALESCE(SUM(bytes), 0)
		FROM events
		WHERE time >= ? AND time < ?
		GROUP BY kind, action, policy_id, in_intf, out_intf
	`, minute, minute, minute+60); err != nil {
		return fmt.Errorf("failed to rollup events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to rollup events: %w", err)
	}
	return nil
}

// Retention deletes events older than N days.
func (s *Store) Retention(days int) error {
	// Calculate the cutoff time
	cutoff := time.Now().AddDate(0, 0, -days).Unix()

	// Delete old events
	_, err := s.db.Exec("DELETE FROM events WHERE time < ?", cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete old events: %w", err)
	}

	return nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// CountByAction counts the events of kind with time >= since (Unix seconds),
// per action, e.g. {"alert": 12, "drop": 3} for kind "ips".
func (s *Store) CountByAction(kind string, since int64) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT COALESCE(action, ''), COUNT(*) FROM events WHERE kind = ? AND time >= ? GROUP BY 1`, kind, since)
	if err != nil {
		return nil, fmt.Errorf("failed to count events: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var action string
		var n int64
		if err := rows.Scan(&action, &n); err != nil {
			return nil, fmt.Errorf("failed to count events: %w", err)
		}
		out[action] = n
	}
	return out, rows.Err()
}
