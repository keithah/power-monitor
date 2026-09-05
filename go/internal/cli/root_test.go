package cli

import (
	"testing"
	"time"
)

func TestParseTimeRejectsInvalidRFC3339(t *testing.T) {
	if _, err := parseTime("not-a-time"); err == nil {
		t.Fatal("invalid RFC3339 timestamp accepted")
	}
}

func TestParseTimeAcceptsRFC3339(t *testing.T) {
	got, err := parseTime("2026-09-03T12:00:00Z")
	if err != nil || !got.Equal(time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("timestamp=%v err=%v", got, err)
	}
}
