package config

import (
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"os"
	"strings"
	"testing"
)

func TestResolveCredentialSupportsLegacyProviderEnvironment(t *testing.T) {
	t.Setenv("ENPHASE_USERNAME", "legacy-user")
	t.Setenv("ENPHASE_PASSWORD", "legacy-pass")
	got, err := ResolveCredential(domain.Setup{Name: "solar", Provider: "enphase", CredentialEnv: "ENPHASE_CREDENTIALS"})
	if err != nil {
		t.Fatal(err)
	}
	if got == "legacy-pass" || got == "" {
		t.Fatalf("expected structured legacy credentials, got %q", got)
	}
	if !strings.Contains(got, "legacy-user") {
		t.Fatalf("credential does not contain username: %q", got)
	}
	_ = os.Unsetenv("ENPHASE_CREDENTIALS")
}
