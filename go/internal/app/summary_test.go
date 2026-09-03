package app

import (
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/store"
)

func TestSummaryCountsOnlyCompletedEmporiaIntervalsAndMarksUnmatchedCoverage(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/power.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	parse := func(v string) time.Time {
		x, e := time.Parse(time.RFC3339, v)
		if e != nil {
			t.Fatal(e)
		}
		return x
	}
	rows := []domain.Reading{
		// Enphase is a current-day cumulative snapshot, not a completed interval.
		{Provider: "enphase", Setup: "solar", Channel: "production", Role: domain.Generation, Timestamp: parse("2026-09-01T18:00:00Z"), KWh: 10},
		// Only the completed 10:00-11:00 mains interval may contribute.
		{Provider: "emporia", Setup: "main", Channel: "Mains", Role: domain.Mains, Timestamp: parse("2026-09-01T10:00:00Z"), WindowStart: parse("2026-09-01T10:00:00Z"), WindowEnd: parse("2026-09-01T11:00:00Z"), KWh: 2},
		{Provider: "emporia", Setup: "main", Channel: "Branch_HVAC", Role: domain.Branch, Timestamp: parse("2026-09-01T10:00:00Z"), WindowStart: parse("2026-09-01T10:00:00Z"), WindowEnd: parse("2026-09-01T11:00:00Z"), KWh: 1},
		// Open 18:00-19:00 bucket is excluded at now=18:30.
		{Provider: "emporia", Setup: "main", Channel: "Mains", Role: domain.Mains, Timestamp: parse("2026-09-01T18:00:00Z"), WindowStart: parse("2026-09-01T18:00:00Z"), WindowEnd: parse("2026-09-01T19:00:00Z"), KWh: 9},
		// PG&E signed net energy is preserved separately, not called consumption.
		{Provider: "pge", Setup: "pge-home", Identity: "electric", Channel: "net_energy", Role: domain.Utility, Timestamp: parse("2026-09-01T12:00:00Z"), WindowStart: parse("2026-09-01T00:00:00Z"), WindowEnd: parse("2026-09-01T12:00:00Z"), KWh: -3},
	}
	if _, err = st.Put(rows); err != nil {
		t.Fatal(err)
	}
	a := New(domain.Config{}, st)
	a.Now = func() time.Time { return parse("2026-09-01T18:30:00Z") }
	buckets, err := a.Summary("day", parse("2026-09-01T00:00:00Z"), parse("2026-09-01T23:59:59Z"))
	if err != nil || len(buckets) != 1 {
		t.Fatalf("buckets=%+v err=%v", buckets, err)
	}
	b := buckets[0]
	if b.GenerationSnapshotKWh != 10 || b.CompletedConsumptionKWh != 2 || b.PGENetEnergyKWh != -3 {
		t.Fatalf("bucket=%+v", b)
	}
	if b.BranchesKWh["main:Branch_HVAC"] != 1 {
		t.Fatalf("branches=%+v", b.BranchesKWh)
	}
	if b.BalanceAvailable || b.Coverage["enphase"] != "snapshot_current_day" || b.Coverage["emporia"] != "partial" || b.Coverage["pge"] != "partial" {
		t.Fatalf("coverage=%+v", b)
	}
}

func TestSummaryUsesMondayWeekAndCalendarMonthBuckets(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/power.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	parse := func(v string) time.Time { x, _ := time.Parse(time.RFC3339, v); return x }
	if _, err = st.Put([]domain.Reading{
		{Provider: "pge", Setup: "home", Channel: "net_energy", Role: domain.Utility, Timestamp: parse("2026-08-31T12:00:00Z"), WindowStart: parse("2026-08-31T00:00:00Z"), WindowEnd: parse("2026-08-31T12:00:00Z"), KWh: 1},
		{Provider: "pge", Setup: "home", Channel: "net_energy", Role: domain.Utility, Timestamp: parse("2026-09-01T12:00:00Z"), WindowStart: parse("2026-09-01T00:00:00Z"), WindowEnd: parse("2026-09-01T12:00:00Z"), KWh: 2},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(domain.Config{}, st)
	a.Now = func() time.Time { return parse("2026-09-02T00:00:00Z") }
	week, err := a.Summary("week", time.Time{}, time.Time{})
	if err != nil || len(week) != 1 || week[0].Period != "2026-08-31" || week[0].PGENetEnergyKWh != 3 {
		t.Fatalf("week=%+v err=%v", week, err)
	}
	month, err := a.Summary("month", time.Time{}, time.Time{})
	if err != nil || len(month) != 2 || month[0].Period != "2026-08" || month[1].Period != "2026-09" {
		t.Fatalf("month=%+v err=%v", month, err)
	}
}
