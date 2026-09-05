package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/config"
)

func TestLoadForCollectDryRunDoesNotCreateDatabase(t *testing.T) {
	temp := t.TempDir()
	cfgPath = filepath.Join(temp, "config.json")
	t.Setenv("POWER_MONITOR_DB", filepath.Join(temp, "power-monitor.sqlite"))
	if err := config.Save(cfgPath, config.Config{}); err != nil {
		t.Fatal(err)
	}
	a, err := loadForCollect(true)
	if err != nil {
		t.Fatal(err)
	}
	if a.Store != nil {
		t.Fatal("dry-run collection opened a writable store")
	}
	if _, err := os.Stat(os.Getenv("POWER_MONITOR_DB")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created database: %v", err)
	}
}
