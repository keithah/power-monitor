package config

import (
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"os"
	"testing"
)

func TestRejectsInlineSecretAndResolvesEnv(t *testing.T) {
	c := domain.Config{Setups: []domain.Setup{{Name: "x", Provider: "enphase", CredentialEnv: "ENPHASE_CREDENTIALS"}}}
	os.Setenv("ENPHASE_CREDENTIALS", "placeholder")
	defer os.Unsetenv("ENPHASE_CREDENTIALS")
	if e := Validate(c); e != nil {
		t.Fatal(e)
	}
	v, _ := ResolveCredential(c.Setups[0])
	if v != "placeholder" {
		t.Fatal("not resolved")
	}
}
func TestSaveDoesNotPersistSecret(t *testing.T) {
	p := t.TempDir() + "/config.json"
	c := domain.Config{Setups: []domain.Setup{{Name: "x", Provider: "emporia", CredentialEnv: "EMPORIA_CREDENTIALS"}}}
	if e := Save(p, c); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) == "" || string(b) == "placeholder" {
		t.Fatal("bad config")
	}
}
