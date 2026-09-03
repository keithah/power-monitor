package store

import (
	"database/sql"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"testing"
	"time"
)

func TestPutIsIdempotent(t *testing.T) {
	s, e := Open(t.TempDir() + "/x.db")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	r := domain.Reading{Provider: "x", Setup: "s", Identity: "i", Channel: "c", Timestamp: time.Now().UTC()}
	a, e := s.Put([]domain.Reading{r})
	if e != nil || a != 1 {
		t.Fatalf("%d %v", a, e)
	}
	a, e = s.Put([]domain.Reading{r})
	if e != nil || a != 0 {
		t.Fatalf("duplicate %d %v", a, e)
	}
}

func TestListFilteredAppliesProviderSetupAndTimeRange(t *testing.T) {
	s, err := Open(t.TempDir() + "/x.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	rs := []domain.Reading{{Provider: "enphase", Setup: "solar", Channel: "production", Timestamp: base, KWh: 1}, {Provider: "emporia", Setup: "panel", Channel: "Mains", Timestamp: base.Add(time.Hour), KWh: 2}}
	if _, err = s.Put(rs); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListFiltered("emporia", "panel", base, base.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].KWh != 2 {
		t.Fatalf("unexpected filtered readings: %#v", got)
	}
}

func TestOpenMigratesAndReadsLegacyNullWindows(t *testing.T) {
	path := t.TempDir() + "/legacy.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE readings (k TEXT PRIMARY KEY, provider TEXT NOT NULL, setup TEXT NOT NULL, identity TEXT, channel TEXT NOT NULL, role TEXT, ts TEXT NOT NULL, watts REAL, kwh REAL); INSERT INTO readings VALUES ('legacy','pge','home','account','interval','utility','2026-09-02T07:00:00Z',0,-1)`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.List()
	if err != nil || len(got) != 1 || !got[0].WindowStart.IsZero() || got[0].Channel != "interval" {
		t.Fatalf("readings=%+v err=%v", got, err)
	}
}
