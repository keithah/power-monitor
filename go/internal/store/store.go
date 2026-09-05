package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, os.ErrInvalid
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS readings (k TEXT PRIMARY KEY, provider TEXT NOT NULL, setup TEXT NOT NULL, identity TEXT, channel TEXT NOT NULL, role TEXT, ts TEXT NOT NULL, ts_unix INTEGER, window_start TEXT, window_end TEXT, watts REAL, kwh REAL, unit TEXT)`); err != nil {
		db.Close()
		return nil, err
	}
	for _, column := range []struct{ name, definition string }{
		{"ts_unix", "INTEGER"}, {"window_start", "TEXT"}, {"window_end", "TEXT"}, {"unit", "TEXT"},
	} {
		if err = ensureColumn(db, column.name, column.definition); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err = backfillTimestampEpoch(db); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func ensureColumn(db *sql.DB, name, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(readings)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var column, kind string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &column, &kind, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if column == name {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf(`ALTER TABLE readings ADD COLUMN %s %s`, name, definition))
	return err
}

func backfillTimestampEpoch(db *sql.DB) error {
	rows, err := db.Query(`SELECT k, ts FROM readings WHERE ts_unix IS NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	updates := make([]struct {
		key  string
		unix int64
	}, 0)
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return err
		}
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return fmt.Errorf("parse legacy reading timestamp %q: %w", raw, err)
		}
		updates = append(updates, struct {
			key  string
			unix int64
		}{key, t.UnixNano()})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, update := range updates {
		if _, err = tx.Exec(`UPDATE readings SET ts_unix = ? WHERE k = ?`, update.unix, update.key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Put(rs []domain.Reading) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, r := range rs {
		res, err := tx.Exec(`INSERT OR IGNORE INTO readings(k,provider,setup,identity,channel,role,ts,ts_unix,window_start,window_end,watts,kwh,unit) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.Key(), r.Provider, r.Setup, r.Identity, r.Channel, r.Role, r.Timestamp.UTC().Format(time.RFC3339Nano), r.Timestamp.UTC().UnixNano(), formatTime(r.WindowStart), formatTime(r.WindowEnd), r.Watts, r.KWh, r.Unit)
		if err != nil {
			return n, err
		}
		x, _ := res.RowsAffected()
		n += int(x)
	}
	if err = tx.Commit(); err != nil {
		return n, err
	}
	return n, nil
}

func (s *Store) List() ([]domain.Reading, error) {
	return s.ListFiltered("", "", time.Time{}, time.Time{})
}

func (s *Store) ListFiltered(provider, setup string, from, to time.Time) ([]domain.Reading, error) {
	query := `SELECT provider,setup,COALESCE(identity,''),channel,COALESCE(role,''),ts,COALESCE(window_start,''),COALESCE(window_end,''),watts,kwh,COALESCE(unit,'') FROM readings WHERE 1=1`
	args := []any{}
	if provider != "" {
		query += " AND provider = ?"
		args = append(args, provider)
	}
	if setup != "" {
		query += " AND setup = ?"
		args = append(args, setup)
	}
	if !from.IsZero() {
		query += " AND ts_unix >= ?"
		args = append(args, from.UTC().UnixNano())
	}
	if !to.IsZero() {
		query += " AND ts_unix <= ?"
		args = append(args, to.UTC().UnixNano())
	}
	query += " ORDER BY ts_unix, k"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Reading{}
	for rows.Next() {
		var r domain.Reading
		var role, ts, start, end string
		if err = rows.Scan(&r.Provider, &r.Setup, &r.Identity, &r.Channel, &role, &ts, &start, &end, &r.Watts, &r.KWh, &r.Unit); err != nil {
			return nil, err
		}
		r.Role = domain.Role(role)
		r.Timestamp, err = time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return nil, err
		}
		r.WindowStart = parseTime(start)
		r.WindowEnd = parseTime(end)
		out = append(out, r)
	}
	return out, rows.Err()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}
