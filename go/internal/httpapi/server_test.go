package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/app"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/client"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/store"
)

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

	r := httptest.NewRequest(http.MethodPost, "/api/collect", nil).WithContext(context.Background())
	w := httptest.NewRecorder()
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
