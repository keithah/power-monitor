package app

import (
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/store"
)

func TestSummaryMarksMatchingCompletedWindowsBalanceAvailable(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/power.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	rows := []domain.Reading{
		{Provider: "enphase", Setup: "solar", Channel: "production", Role: domain.Generation, Timestamp: end, WindowStart: start, WindowEnd: end, KWh: 3},
		{Provider: "enphase", Setup: "solar", Channel: "production", Role: domain.Generation, Timestamp: end.Add(time.Hour), WindowStart: end, WindowEnd: end.Add(time.Hour), KWh: 4},
		{Provider: "emporia", Setup: "main", Channel: "Mains", Role: domain.Mains, Timestamp: end, WindowStart: start, WindowEnd: end, KWh: 2},
		{Provider: "emporia", Setup: "main", Channel: "Mains", Role: domain.Mains, Timestamp: end.Add(time.Hour), WindowStart: end, WindowEnd: end.Add(time.Hour), KWh: 3},
		{Provider: "pge", Setup: "utility", Channel: "net_energy", Role: domain.Utility, Timestamp: end, WindowStart: start, WindowEnd: end, KWh: -1},
		{Provider: "pge", Setup: "utility", Channel: "net_energy", Role: domain.Utility, Timestamp: end.Add(time.Hour), WindowStart: end, WindowEnd: end.Add(time.Hour), KWh: -2},
	}
	if _, err := st.Put(rows); err != nil {
		t.Fatal(err)
	}
	a := New(domain.Config{}, st)
	a.Now = func() time.Time { return end.Add(2 * time.Hour) }
	got, err := a.Summary("day", start, end.Add(time.Hour))
	if err != nil || len(got) != 1 {
		t.Fatalf("summary=%+v err=%v", got, err)
	}
	if got[0].GenerationSnapshotKWh != 7 {
		t.Fatalf("interval generation must sum, got %+v", got[0])
	}
	if !got[0].BalanceAvailable {
		t.Fatalf("expected matching completed coverage: %+v", got[0])
	}
	for _, provider := range []string{"enphase", "emporia", "pge"} {
		if got[0].Coverage[provider] != "complete" {
			t.Fatalf("%s coverage=%q", provider, got[0].Coverage[provider])
		}
	}
}
