package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type Role string

const (
	Generation Role = "generation"
	Mains      Role = "mains"
	Subpanel   Role = "subpanel"
	Utility    Role = "utility"
	Branch     Role = "branch"
)

type Reading struct {
	Provider, Setup, Identity, Channel string
	Role                               Role
	Timestamp                          time.Time
	WindowStart, WindowEnd             time.Time
	Watts, KWh                         float64
	Unit                               string
}

func (r Reading) Key() string {
	return strings.Join([]string{r.Provider, r.Setup, r.Identity, r.Channel, r.Timestamp.UTC().Format(time.RFC3339Nano)}, "|")
}
func SortReadings(rs []Reading) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Key() < rs[j].Key() })
}

type Setup struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	SiteID        string `json:"site_id,omitempty"`
	DeviceGID     string `json:"device_gid,omitempty"`
	AccountID     string `json:"account_id,omitempty"`
	Utility       string `json:"utility,omitempty"`
	CredentialEnv string `json:"credential_env"`
	Role          Role   `json:"role"`
	Parent        string `json:"parent,omitempty"`
}
type Rollup struct {
	Name             string
	Include, Exclude []string
	Override         bool
}
type Config struct {
	Setups  []Setup
	Rollups []Rollup
}

func ValidateRollup(c Config, r Rollup) error {
	seen := map[string]bool{}
	for _, ref := range r.Include {
		if seen[ref] {
			return fmt.Errorf("duplicate input %q", ref)
		}
		seen[ref] = true
		if !exists(c, ref) {
			return fmt.Errorf("missing input %q", ref)
		}
	}
	for _, s := range c.Setups {
		if s.Parent != "" {
			p := false
			child := false
			for _, x := range r.Include {
				if x == s.Parent+":Mains" {
					p = true
				}
				if x == s.Name+":Mains" {
					child = true
				}
			}
			if p && child && !r.Override {
				return fmt.Errorf("rollup %q combines parent %s and contained child %s; explicit override required", r.Name, s.Parent, s.Name)
			}
		}
	}
	return nil
}
func exists(c Config, ref string) bool {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 {
		return false
	}
	for _, s := range c.Setups {
		if s.Name == parts[0] {
			return true
		}
	}
	return false
}
func Aggregate(c Config, r Rollup, readings []Reading) (float64, error) {
	if err := ValidateRollup(c, r); err != nil {
		return 0, err
	}
	excluded := map[string]bool{}
	for _, x := range r.Exclude {
		excluded[x] = true
	}
	total := 0.0
	for _, x := range r.Include {
		for _, v := range readings {
			if v.Setup+":"+v.Channel == x && !excluded[x] {
				total += v.KWh
			}
		}
	}
	return total, nil
}
