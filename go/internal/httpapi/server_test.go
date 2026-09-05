package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/app"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/client"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/store"
)

type recordingMFASession struct {
	starts   int
	selects  []string
	verifies []string
}

func (m *recordingMFASession) StartMFA(context.Context) ([]client.MFAOption, error) {
	m.starts++
	return []client.MFAOption{{Label: "Email", Value: "Email"}}, nil
}

func (m *recordingMFASession) SelectMFA(_ context.Context, option string) error {
	m.selects = append(m.selects, option)
	return nil
}

func (m *recordingMFASession) VerifyMFA(_ context.Context, code string) error {
	m.verifies = append(m.verifies, code)
	return nil
}

func TestHealthStatusUsageReportAndCollectCompatibility(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/power.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.Put([]domain.Reading{{Provider: "emporia", Setup: "main", Channel: "Mains", Role: domain.Mains, Timestamp: time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC), KWh: 2, Unit: "kWh"}}); err != nil {
		t.Fatal(err)
	}
	a := app.New(domain.Config{Setups: []domain.Setup{{Name: "main", Provider: "emporia", CredentialEnv: "EMPORIA_CREDENTIAL"}}}, st)
	a.Providers["main"] = client.Mock{Readings: []domain.Reading{{Provider: "emporia", Setup: "main", Channel: "Mains", Role: domain.Mains, Timestamp: time.Date(2026, 9, 3, 11, 0, 0, 0, time.UTC), KWh: 3, Unit: "kWh"}}}
	h := New(a)

	for _, tc := range []struct {
		path     string
		want     int
		contains string
	}{
		{"/health", http.StatusOK, `"ok":true`},
		{"/api/status", http.StatusOK, `"service":"power-monitor"`},
		{"/api/usage?provider=emporia", http.StatusOK, `"provider":"emporia"`},
		{"/api/report", http.StatusOK, `"rows"`},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want || !bytes.Contains(w.Body.Bytes(), []byte(tc.contains)) {
			t.Fatalf("%s: code=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}

	var status map[string]any
	r := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status) != 3 || status["version"] != float64(1) || status["service"] != "power-monitor" || status["providers"] == nil {
		t.Fatalf("unexpected status: %#v", status)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/report", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var report struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil || len(report.Rows) == 0 {
		t.Fatalf("report=%s err=%v", w.Body.String(), err)
	}
	for _, key := range []string{"ts", "source", "channel", "watts", "kwh", "raw"} {
		if _, ok := report.Rows[0][key]; !ok {
			t.Fatalf("report missing %q: %#v", key, report.Rows[0])
		}
	}

	r = httptest.NewRequest(http.MethodPost, "/api/collect", nil).WithContext(context.Background())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("collect code=%d body=%s", w.Code, w.Body.String())
	}
	var result map[string]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result["main"]["status"] != "ok" {
		t.Fatalf("collect=%s err=%v", w.Body.String(), err)
	}
}

func TestUsageRejectsInvalidLimitAndMFAPreconditions(t *testing.T) {
	h := New(app.New(domain.Config{}, nil))
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodGet, "/api/usage?limit=bad", "", http.StatusBadRequest},
		{http.MethodPost, "/api/pge/mfa/select", `{"option":"Email"}`, http.StatusConflict},
		{http.MethodPost, "/api/pge/mfa/verify", `{"code":"123456"}`, http.StatusConflict},
	} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s: code=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

type cancellationMFASession struct{ verifyErr error }

func (m *cancellationMFASession) StartMFA(context.Context) ([]client.MFAOption, error) {
	return nil, nil
}
func (m *cancellationMFASession) SelectMFA(context.Context, string) error { return nil }
func (m *cancellationMFASession) VerifyMFA(ctx context.Context, _ string) error {
	m.verifyErr = ctx.Err()
	return nil
}

type selectFailureMFASession struct{ err error }

func (m selectFailureMFASession) StartMFA(context.Context) ([]client.MFAOption, error) {
	return nil, nil
}
func (m selectFailureMFASession) SelectMFA(context.Context, string) error { return m.err }
func (m selectFailureMFASession) VerifyMFA(context.Context, string) error { return nil }

type preconditionMFASession struct{}

func (preconditionMFASession) StartMFA(context.Context) ([]client.MFAOption, error) { return nil, nil }
func (preconditionMFASession) SelectMFA(context.Context, string) error              { return nil }
func (preconditionMFASession) VerifyMFA(context.Context, string) error {
	return &client.ProviderError{Class: client.ErrMFARequired, Err: errors.New("select an MFA delivery option before verifying a code")}
}

type blockingHTTPMFASession struct {
	entered chan struct{}
	release chan struct{}
}

func (m *blockingHTTPMFASession) StartMFA(context.Context) ([]client.MFAOption, error) {
	close(m.entered)
	<-m.release
	return []client.MFAOption{{Label: "Email", Value: "Email"}}, nil
}
func (m *blockingHTTPMFASession) SelectMFA(context.Context, string) error { return nil }
func (m *blockingHTTPMFASession) VerifyMFA(context.Context, string) error { return nil }

func TestMFAEndpointsRouteExplicitPGESetupAndRejectAmbiguity(t *testing.T) {
	north := &recordingMFASession{}
	south := &recordingMFASession{}
	a := app.New(domain.Config{Setups: []domain.Setup{
		{Name: "north", Provider: "pge", CredentialEnv: "PGE_NORTH"},
		{Name: "south", Provider: "opower", CredentialEnv: "PGE_SOUTH"},
	}}, nil)
	a.Providers["north"] = &client.Opower{MFA: north}
	a.Providers["south"] = &client.Opower{MFA: south}
	h := New(a)

	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/pge/mfa/start?setup=south", "", http.StatusOK},
		{http.MethodPost, "/api/pge/mfa/select?setup=south", `{"option":"Email"}`, http.StatusOK},
		{http.MethodPost, "/api/pge/mfa/verify?setup=south", `{"code":"123456"}`, http.StatusOK},
		{http.MethodPost, "/api/pge/mfa/start", "", http.StatusBadRequest},
	} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s: code=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if north.starts != 0 || len(north.selects) != 0 || len(north.verifies) != 0 {
		t.Fatalf("north setup received MFA calls: %+v", north)
	}
	if south.starts != 1 || len(south.selects) != 1 || len(south.verifies) != 1 {
		t.Fatalf("south setup did not receive all MFA calls: %+v", south)
	}
}

func TestSelectMFAReportsUpstreamFailuresAsBadGateway(t *testing.T) {
	a := app.New(domain.Config{Setups: []domain.Setup{{Name: "home", Provider: "pge", CredentialEnv: "PGE_HOME"}}}, nil)
	a.Providers["home"] = &client.Opower{MFA: selectFailureMFASession{err: &client.ProviderError{Class: client.ErrUnavailable, Err: errors.New("network unavailable")}}}
	h := New(a)
	r := httptest.NewRequest(http.MethodPost, "/api/pge/mfa/select", bytes.NewBufferString(`{"option":"Email"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("select code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVerifyMFARejectsConfiguredUnselectedChallenge(t *testing.T) {
	a := app.New(domain.Config{Setups: []domain.Setup{{Name: "home", Provider: "pge", CredentialEnv: "PGE_HOME"}}}, nil)
	a.Providers["home"] = &client.Opower{MFA: preconditionMFASession{}}
	h := New(a)
	r := httptest.NewRequest(http.MethodPost, "/api/pge/mfa/verify", bytes.NewBufferString(`{"code":"123456"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("verify code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestVerifyMFARejectsMalformedCodes(t *testing.T) {
	a := app.New(domain.Config{Setups: []domain.Setup{{Name: "home", Provider: "pge", CredentialEnv: "PGE_HOME"}}}, nil)
	a.Providers["home"] = &client.Opower{MFA: &recordingMFASession{}}
	h := New(a)
	for _, code := range []string{"", "123", "abcde", "123456789", "12 34"} {
		r := httptest.NewRequest(http.MethodPost, "/api/pge/mfa/verify", bytes.NewBufferString(`{"code":"`+code+`"}`))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("code %q: got %d body=%s", code, w.Code, w.Body.String())
		}
	}
}

func TestVerifyMFAUsesRequestContext(t *testing.T) {
	mfa := &cancellationMFASession{}
	a := app.New(domain.Config{Setups: []domain.Setup{{Name: "home", Provider: "pge", CredentialEnv: "PGE_HOME"}}}, nil)
	a.Providers["home"] = &client.Opower{MFA: mfa}
	h := New(a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodPost, "/api/pge/mfa/verify", bytes.NewBufferString(`{"code":"123456"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || mfa.verifyErr != context.Canceled {
		t.Fatalf("verify code=%d context error=%v", w.Code, mfa.verifyErr)
	}
}

func TestVerifyMFACancelsWhileWaitingForSetupLock(t *testing.T) {
	mfa := &blockingHTTPMFASession{entered: make(chan struct{}), release: make(chan struct{})}
	a := app.New(domain.Config{Setups: []domain.Setup{{Name: "home", Provider: "pge", CredentialEnv: "PGE_HOME"}}}, nil)
	a.Providers["home"] = &client.Opower{MFA: mfa}
	h := New(a)
	startDone := make(chan error, 1)
	go func() { _, err := a.StartMFA(context.Background(), "home"); startDone <- err }()
	<-mfa.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodPost, "/api/pge/mfa/verify", bytes.NewBufferString(`{"code":"123456"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, r); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("verify did not honor cancellation while waiting for the setup lock")
	}
	if w.Code != http.StatusBadGateway {
		t.Fatalf("verify code=%d body=%s", w.Code, w.Body.String())
	}
	close(mfa.release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
}
