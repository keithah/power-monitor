package config

import (
	"encoding/json"
	"fmt"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"os"
	"path/filepath"
	"strings"
)

type Config = domain.Config
type Setup = domain.Setup
type Rollup = domain.Rollup

func DefaultPath() string {
	if p := os.Getenv("POWER_MONITOR_CONFIG"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "power-monitor", "config.json")
}
func DataDir() string {
	if p := os.Getenv("POWER_MONITOR_DATA_DIR"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "power-monitor")
}
func DBPath() string {
	if p := os.Getenv("POWER_MONITOR_DB"); p != "" {
		return p
	}
	return filepath.Join(DataDir(), "power-monitor.sqlite")
}
func Load(path string) (Config, error) {
	var c Config
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	return c, e
}
func Save(path string, c Config) error {
	if e := Validate(c); e != nil {
		return e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, append(b, '\n'), 0600); e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e == nil {
		_ = os.Chmod(path, 0600)
	}
	return e
}
func Validate(c Config) error {
	names := map[string]bool{}
	for _, s := range c.Setups {
		if s.Name == "" || s.Provider == "" {
			return fmt.Errorf("setup name and provider required")
		}
		if names[s.Name] {
			return fmt.Errorf("duplicate setup %q", s.Name)
		}
		names[s.Name] = true
		if s.CredentialEnv == "" || strings.Contains(strings.ToLower(s.CredentialEnv), "password") || strings.Contains(strings.ToLower(s.CredentialEnv), "token") {
			return fmt.Errorf("setup %q requires safe credential_env reference", s.Name)
		}
	}
	for _, r := range c.Rollups {
		if e := domain.ValidateRollup(c, r); e != nil {
			return e
		}
	}
	return nil
}
func ResolveCredential(s Setup) (string, error) {
	if s.CredentialEnv == "" {
		return "", fmt.Errorf("missing credential_env")
	}
	if v, ok := os.LookupEnv(s.CredentialEnv); ok && v != "" {
		return v, nil
	}
	// Keep compatibility with the deployed Python service, whose credentials are
	// split into provider-specific environment variables rather than one blob.
	legacy := map[string][]string{
		"enphase": {"ENPHASE_USERNAME", "ENPHASE_EMAIL", "ENPHASE_PASSWORD"},
		"emporia": {"EMPORIA_EMAIL", "EMPORIA_PASSWORD"},
		"opower":  {"PGE_USERNAME", "PGE_EMAIL", "PGE_PASSWORD"},
		"pge":     {"PGE_USERNAME", "PGE_EMAIL", "PGE_PASSWORD"},
	}[strings.ToLower(s.Provider)]
	values := map[string]string{}
	found := false
	for _, key := range legacy {
		if v := os.Getenv(key); v != "" {
			values[key] = v
			found = true
		}
	}
	if found {
		b, _ := json.Marshal(values)
		return string(b), nil
	}
	return "", fmt.Errorf("credential environment %s is not set", s.CredentialEnv)
}
func Select(c Config, name string) (Setup, error) {
	if name != "" {
		for _, s := range c.Setups {
			if s.Name == name {
				return s, nil
			}
		}
		var names []string
		for _, s := range c.Setups {
			names = append(names, s.Name)
		}
		return Setup{}, fmt.Errorf("setup %q not found (candidates: %s)", name, strings.Join(names, ", "))
	}
	if len(c.Setups) != 1 {
		return Setup{}, fmt.Errorf("selector is ambiguous; specify setup")
	}
	return c.Setups[0], nil
}
