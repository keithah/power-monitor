package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
)

// SummaryBucket reports provider measurements without fabricating a cross-provider
// energy balance. A balance can only be exposed when matching, complete windows
// from every provider have been collected.
type SummaryBucket struct {
	Period                  string             `json:"period"`
	GenerationSnapshotKWh   float64            `json:"enphase_generation_snapshot_kwh"`
	CompletedConsumptionKWh float64            `json:"emporia_completed_consumption_kwh"`
	PGENetEnergyKWh         float64            `json:"pge_net_energy_kwh"`
	BranchesKWh             map[string]float64 `json:"emporia_branches_kwh,omitempty"`
	Coverage                map[string]string  `json:"coverage"`
	BalanceAvailable        bool               `json:"balance_available"`
	ReadingCount            int                `json:"reading_count"`
}

type summaryValue struct {
	at  time.Time
	kwh float64
}

func (a *App) Summary(period string, from, to time.Time) ([]SummaryBucket, error) {
	period = strings.ToLower(strings.TrimSpace(period))
	if period == "" {
		period = "day"
	}
	if period != "day" && period != "week" && period != "month" && period != "year" {
		return nil, fmt.Errorf("period must be day, week, month, or year")
	}
	now := time.Now().UTC()
	if a.Now != nil {
		now = a.Now().UTC()
	}
	rows := a.ReadingsFiltered("", "", from, to)
	buckets := map[string]*SummaryBucket{}
	snapshots := map[string]summaryValue{}
	for _, r := range rows {
		key := summaryPeriod(r.Timestamp, period)
		b := buckets[key]
		if b == nil {
			b = &SummaryBucket{Period: key, BranchesKWh: map[string]float64{}, Coverage: map[string]string{"enphase": "absent", "emporia": "absent", "pge": "absent"}}
			buckets[key] = b
		}
		b.ReadingCount++
		switch r.Provider {
		case "enphase":
			b.Coverage["enphase"] = "snapshot_current_day"
			source := strings.Join([]string{key, r.Setup, r.Identity, r.Channel}, "\x00")
			old, ok := snapshots[source]
			if ok && !r.Timestamp.After(old.at) {
				continue
			}
			if ok {
				b.GenerationSnapshotKWh -= old.kwh
			}
			snapshots[source] = summaryValue{r.Timestamp, r.KWh}
			b.GenerationSnapshotKWh += r.KWh
		case "emporia":
			if r.WindowEnd.IsZero() || r.WindowEnd.After(now) {
				if b.Coverage["emporia"] == "absent" {
					b.Coverage["emporia"] = "partial"
				}
				continue
			}
			if b.Coverage["emporia"] == "absent" {
				b.Coverage["emporia"] = "partial"
			}
			if r.Role == domain.Mains {
				b.CompletedConsumptionKWh += r.KWh
			}
			if r.Role == domain.Branch || r.Role == domain.Subpanel {
				b.BranchesKWh[r.Setup+":"+r.Channel] += r.KWh
			}
		case "pge", "opower":
			if r.WindowEnd.IsZero() || r.WindowEnd.After(now) {
				if b.Coverage["pge"] == "absent" {
					b.Coverage["pge"] = "partial"
				}
				continue
			}
			if b.Coverage["pge"] == "absent" {
				b.Coverage["pge"] = "partial"
			}
			b.PGENetEnergyKWh += r.KWh
		}
	}
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]SummaryBucket, 0, len(keys))
	for _, k := range keys {
		b := *buckets[k]
		if len(b.BranchesKWh) == 0 {
			b.BranchesKWh = nil
		}
		out = append(out, b)
	}
	return out, nil
}

func summaryPeriod(t time.Time, period string) string {
	t = t.UTC()
	switch period {
	case "week":
		weekday := int(t.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return t.AddDate(0, 0, -(weekday - 1)).Format("2006-01-02")
	case "month":
		return t.Format("2006-01")
	case "year":
		return t.Format("2006")
	default:
		return t.Format("2006-01-02")
	}
}
