package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmporiaRefreshesExpiredCachedCredential(t *testing.T) {
	authCalls, usageCalls := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cognito":
			authCalls++
			_, _ = w.Write([]byte(`{"AuthenticationResult":{"IdToken":"fresh"}}`))
		case "/v1/customers/devices/usages":
			usageCalls++
			if r.Header.Get("authtoken") != "fresh" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"instant":"2026-01-01T01:00:00Z","device_usages":[{"channel_usages":[{"channel_id":"Mains","usage":2}]}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	e := &Emporia{Client: Client{BaseURL: srv.URL, HTTP: srv.Client(), Credentials: "expired"}, Email: "email", Password: "password", CognitoURL: srv.URL + "/cognito", ClientID: "client"}
	readings, err := e.Usages(context.Background(), "42")
	if err != nil || len(readings) != 1 {
		t.Fatalf("readings=%#v err=%v", readings, err)
	}
	if authCalls != 1 || usageCalls != 2 {
		t.Fatalf("authCalls=%d usageCalls=%d", authCalls, usageCalls)
	}
}

func TestCanonicalMFAOption(t *testing.T) {
	for input, want := range map[string]string{" email ": "Email", "PHONE": "Phone", "mail": ""} {
		if got := canonicalMFAOption(input); got != want {
			t.Errorf("canonicalMFAOption(%q)=%q want %q", input, got, want)
		}
	}
}
