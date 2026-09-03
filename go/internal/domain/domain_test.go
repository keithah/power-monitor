package domain

import (
	"testing"
	"time"
)

func TestParentChildRollupRequiresOverride(t *testing.T) {
	c := Config{Setups: []Setup{{Name: "main", Role: Mains}, {Name: "child", Role: Subpanel, Parent: "main"}}}
	r := Rollup{Name: "whole", Include: []string{"main:Mains", "child:Mains"}}
	if ValidateRollup(c, r) == nil {
		t.Fatal("expected rejection")
	}
	r.Override = true
	if e := ValidateRollup(c, r); e != nil {
		t.Fatal(e)
	}
}
func TestReadingKeyUTCStable(t *testing.T) {
	a := Reading{Provider: "x", Setup: "s", Identity: "i", Channel: "c", Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	b := a
	b.Timestamp = b.Timestamp.In(time.FixedZone("p", -8*3600))
	if a.Key() != b.Key() {
		t.Fatal("keys differ")
	}
}
