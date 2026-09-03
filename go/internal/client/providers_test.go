package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
)

func TestEnphaseLoginPreservesCookieAndUsesForm(t *testing.T) {
	var gotCookie bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login/login.json" {
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Errorf("content type = %q", r.Header.Get("Content-Type"))
			}
			b, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(b))
			if form.Get("user[email]") != "user@example.com" || form.Get("user[password]") != "pw" {
				t.Errorf("form = %v", form)
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
			return
		}
		gotCookie = r.Header.Get("Cookie") == "session=abc"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sites":[{"site_id":"one"}]}`))
	}))
	defer srv.Close()
	hc := srv.Client()
	e := Enphase{Client: Client{BaseURL: srv.URL, HTTP: hc}, Username: "user@example.com", Password: "pw"}
	if err := e.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sites(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !gotCookie {
		t.Fatal("login cookie was not preserved")
	}
}

func TestEnphaseProductionAndMultipleSites(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"stats":[{"production":[12],"totals":{"production":3}}]}`))
	}))
	defer srv.Close()
	e := Enphase{Client: Client{BaseURL: srv.URL, HTTP: srv.Client()}, SiteIDs: []string{"one", "two"}}
	rs, err := e.Collect(context.Background(), domain.Setup{Name: "s", Provider: "enphase"})
	if err != nil || len(rs) != 2 {
		t.Fatalf("enphase %#v %v", rs, err)
	}
	if rs[0].KWh != 0.003 || rs[1].KWh != 0.003 || rs[0].Unit != "kWh" {
		t.Fatalf("production must convert Wh to kWh: %#v", rs)
	}
	if strings.Join(paths, ",") != "/pv/systems/one/today,/pv/systems/two/today" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestEmporiaCognitoAndUsageRequestShape(t *testing.T) {
	var authOK, usageOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/cognito" {
			b, _ := io.ReadAll(r.Body)
			var v map[string]any
			_ = json.Unmarshal(b, &v)
			params := v["AuthParameters"].(map[string]any)
			authOK = v["AuthFlow"] == "USER_PASSWORD_AUTH" && v["ClientId"] == "client" && params["USERNAME"] == "email" && params["PASSWORD"] == "password" && r.Header.Get("X-Amz-Target") == "AWSCognitoIdentityProviderService.InitiateAuth"
			_, _ = w.Write([]byte(`{"AuthenticationResult":{"IdToken":"token"}}`))
			return
		}
		if r.URL.Path == "/v1/customers/devices/usages" {
			usageOK = r.Header.Get("authtoken") == "token" && r.URL.Query().Get("device_gids") == "42" && r.URL.Query().Get("scale") == "HOUR" && r.URL.Query().Get("energy_unit") == "KILOWATT_HOURS" && r.URL.Query().Get("instant") != ""
			_, _ = w.Write([]byte(`{"instant":"2026-01-01T01:00:00Z","device_usages":[{"channel_usages":[{"channel_id":"Mains","usage":2},{"channel_id":"Branch_1","usage":1},{"channel_id":"TotalUsage","usage":3}]}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	e := &Emporia{Client: Client{BaseURL: srv.URL, HTTP: srv.Client()}, Email: "email", Password: "password", CognitoURL: srv.URL + "/cognito", ClientID: "client"}
	rs, err := e.Usages(context.Background(), "42")
	if err != nil || len(rs) != 2 {
		t.Fatalf("emporia %#v %v", rs, err)
	}
	if !authOK || !usageOK {
		t.Fatalf("authOK=%v usageOK=%v", authOK, usageOK)
	}
}

func TestConfiguredUsesProviderSpecificLegacyCredentials(t *testing.T) {
	t.Setenv("ENPHASE_USERNAME", "enphase-user")
	t.Setenv("ENPHASE_PASSWORD", "enphase-password")
	t.Setenv("ENPHASE_SYSTEM_IDS", "one,two")
	t.Setenv("EMPORIA_EMAIL", "emporia@example.com")
	t.Setenv("EMPORIA_PASSWORD", "emporia-password")
	t.Setenv("PGE_USERNAME", "pge-user")
	t.Setenv("PGE_PASSWORD", "pge-password")

	p, err := Configured(domain.Setup{Name: "solar", Provider: "enphase", CredentialEnv: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	e := p.(*Enphase)
	if e.Username != "enphase-user" || e.Password != "enphase-password" || strings.Join(e.SiteIDs, ",") != "one,two" {
		t.Fatalf("unexpected Enphase config: user=%q sites=%v", e.Username, e.SiteIDs)
	}
	p, err = Configured(domain.Setup{Name: "panel", Provider: "emporia", CredentialEnv: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	em := p.(*Emporia)
	if em.Email != "emporia@example.com" || em.Password != "emporia-password" {
		t.Fatalf("unexpected Emporia config: email=%q", em.Email)
	}
}
func TestProviderNormalization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/pv/systems/site/today":
			_, _ = w.Write([]byte(`{"stats":[{"production":[12],"totals":{"production":3}}]}`))
		case "/v1/customers/devices/usages":
			_, _ = w.Write([]byte(`{"instant":"2026-01-01T01:00:00Z","device_usages":[{"channel_usages":[{"channel_id":"Mains","usage":2},{"channel_id":"Branch_1","usage":1}]}]}`))
		case "/api/accounts/acct/usage":
			_, _ = w.Write([]byte(`{"intervals":[{"start_time":"2026-01-01T00:00:00Z","end_time":"2026-01-01T01:00:00Z","consumption":4}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	e := Enphase{Client: Client{BaseURL: srv.URL, HTTP: srv.Client()}}
	rs, err := e.Collect(context.Background(), domain.Setup{Name: "s", Provider: "enphase", SiteID: "site"})
	if err != nil || len(rs) != 1 || rs[0].KWh != 0.003 {
		t.Fatalf("enphase %#v %v", rs, err)
	}
}
