package store

import (
	"database/sql"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"
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
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS readings (k TEXT PRIMARY KEY, provider TEXT NOT NULL, setup TEXT NOT NULL, identity TEXT, channel TEXT NOT NULL, role TEXT, ts TEXT NOT NULL, window_start TEXT, window_end TEXT, watts REAL, kwh REAL)`)
	if err == nil {
		_, _ = db.Exec(`ALTER TABLE readings ADD COLUMN window_start TEXT`)
		_, _ = db.Exec(`ALTER TABLE readings ADD COLUMN window_end TEXT`)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
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
		res, err := tx.Exec(`INSERT OR IGNORE INTO readings(k,provider,setup,identity,channel,role,ts,window_start,window_end,watts,kwh) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, r.Key(), r.Provider, r.Setup, r.Identity, r.Channel, r.Role, r.Timestamp.UTC().Format(time.RFC3339Nano), formatTime(r.WindowStart), formatTime(r.WindowEnd), r.Watts, r.KWh)
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
	query := `SELECT provider,setup,COALESCE(identity,''),channel,COALESCE(role,''),ts,COALESCE(window_start,''),COALESCE(window_end,''),watts,kwh FROM readings WHERE 1=1`
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
		query += " AND ts >= ?"
		args = append(args, from.UTC().Format(time.RFC3339Nano))
	}
	if !to.IsZero() {
		query += " AND ts <= ?"
		args = append(args, to.UTC().Format(time.RFC3339Nano))
	}
	query += " ORDER BY k"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Reading{}
	for rows.Next() {
		var r domain.Reading
		var role, ts, start, end string
		if err = rows.Scan(&r.Provider, &r.Setup, &r.Identity, &r.Channel, &role, &ts, &start, &end, &r.Watts, &r.KWh); err != nil {
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
