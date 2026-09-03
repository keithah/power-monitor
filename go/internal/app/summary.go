package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
)

// SummaryBucket reports provider measurements without fabricating a cross-provider
// energy balance. A balance is available only when every provider has a matching,
// completed interval; ordinary Enphase current-day snapshots therefore remain
// explicitly unavailable for energy balancing.
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
type summaryState struct {
	bucket  *SummaryBucket
	windows map[string]map[string]struct{}
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
	states := map[string]*summaryState{}
	snapshots := map[string]summaryValue{}
	for _, r := range a.ReadingsFiltered("", "", from, to) {
		key := summaryPeriod(r.Timestamp, period)
		state := states[key]
		if state == nil {
			state = &summaryState{bucket: &SummaryBucket{Period: key, BranchesKWh: map[string]float64{}, Coverage: map[string]string{"enphase": "absent", "emporia": "absent", "pge": "absent"}}, windows: map[string]map[string]struct{}{}}
			states[key] = state
		}
		b := state.bucket
		b.ReadingCount++
		complete := completedWindow(r, now)
		switch r.Provider {
		case "enphase":
			if r.WindowStart.IsZero() || r.WindowEnd.IsZero() {
				b.Coverage["enphase"] = "snapshot_current_day"
				source := strings.Join([]string{key, r.Setup, r.Identity, r.Channel}, "\x00")
				if old, ok := snapshots[source]; ok && !r.Timestamp.After(old.at) {
					continue
				} else if ok {
					b.GenerationSnapshotKWh -= old.kwh
				}
				snapshots[source] = summaryValue{r.Timestamp, r.KWh}
				b.GenerationSnapshotKWh += r.KWh
				continue
			}
			setCoverage(b, "enphase", complete)
			if !complete {
				continue
			}
			b.GenerationSnapshotKWh += r.KWh
			state.recordWindow("enphase", r)
		case "emporia":
			setCoverage(b, "emporia", complete)
			if !complete {
				continue
			}
			if r.Role == domain.Mains {
				b.CompletedConsumptionKWh += r.KWh
				state.recordWindow("emporia", r)
			}
			if r.Role == domain.Branch || r.Role == domain.Subpanel {
				b.BranchesKWh[r.Setup+":"+r.Channel] += r.KWh
			}
		case "pge", "opower":
			setCoverage(b, "pge", complete)
			if !complete {
				continue
			}
			b.PGENetEnergyKWh += r.KWh
			state.recordWindow("pge", r)
		}
	}
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]SummaryBucket, 0, len(keys))
	for _, k := range keys {
		state := states[k]
		b := *state.bucket
		if len(b.BranchesKWh) == 0 {
			b.BranchesKWh = nil
		}
		b.BalanceAvailable = matchingWindows(state.windows)
		out = append(out, b)
	}
	return out, nil
}

func completedWindow(r domain.Reading, now time.Time) bool {
	return !r.WindowStart.IsZero() && !r.WindowEnd.IsZero() && !r.WindowEnd.After(now)
}
func setCoverage(b *SummaryBucket, provider string, complete bool) {
	if !complete {
		b.Coverage[provider] = "partial"
		return
	}
	if b.Coverage[provider] == "absent" {
		b.Coverage[provider] = "complete"
	}
}
func (s *summaryState) recordWindow(provider string, r domain.Reading) {
	if s.windows[provider] == nil {
		s.windows[provider] = map[string]struct{}{}
	}
	s.windows[provider][r.WindowStart.UTC().Format(time.RFC3339Nano)+"/"+r.WindowEnd.UTC().Format(time.RFC3339Nano)] = struct{}{}
}
func matchingWindows(windows map[string]map[string]struct{}) bool {
	for _, provider := range []string{"enphase", "emporia", "pge"} {
		if len(windows[provider]) == 0 {
			return false
		}
	}
	for key := range windows["enphase"] {
		if _, ok := windows["emporia"][key]; ok {
			if _, ok := windows["pge"][key]; ok {
				return true
			}
		}
	}
	return false
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
