package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
)

func TestOpowerPGESessionAccountsAndIntervals(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	var sawLogin, sawRedirect, sawHome, sawAura, sawBrowser, sawCustomers, sawReads bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/myaccount/s/sfsites/aura":
			b, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(b))
			var msg map[string]any
			_ = json.Unmarshal([]byte(form.Get("message")), &msg)
			if strings.Contains(form.Get("message"), "customLoginLWCController") {
				sawLogin = r.Method == http.MethodPost && msg["actions"] != nil
				sawAura = form.Get("aura.pageURI") == "/myaccount/s/login/" && form.Get("aura.token") == "null"
			}
			if strings.Contains(form.Get("message"), "customLoginLWCController") {
				http.SetCookie(w, &http.Cookie{Name: "__Host-ERIC_PROD_test", Value: "aura", Path: "/", Secure: true})
				_, _ = w.Write([]byte(`{"actions":[{"state":"SUCCESS","returnValue":{"returnValue":{"retMessage":"http://` + r.Host + `/redirect"}}}]}`))
			} else {
				_, _ = w.Write([]byte(`{"actions":[{"state":"SUCCESS","returnValue":{"returnValue":{}}}]}`))
			}
		case "/redirect":
			sawRedirect = r.Header.Get("Cookie") != ""
		case "/myaccount/s/":
			sawHome = r.Header.Get("Cookie") != ""
		case "/myaccount/apex/MyAcct_VF_BillInsights_OpowerDataBrowser":
			sawBrowser = true
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("let tokenFromApex = 'opower-token'"))
		case "/ei/edge/apis/multi-account-v1/cws/pge/customers":
			sawCustomers = r.Header.Get("Authorization") == "Bearer opower-token" && r.Header.Get("Opower-Selected-Entities") == ""
			_, _ = w.Write([]byte(`{"customers":[{"uuid":"customer","utilityAccounts":[{"uuid":"acct-uuid","preferredUtilityAccountId":"acct","name":"home","readResolution":"DAY"}]}]}`))
		case "/ei/edge/apis/DataBrowser-v1/cws/utilities/pge/utilityAccounts/acct-uuid/reads":
			sawReads = r.Header.Get("Authorization") == "Bearer opower-token" && r.URL.Query().Get("aggregateType") == "day" && r.URL.Query().Get("startDate") == "2026-01-01" && r.URL.Query().Get("endDate") == "2026-01-03"
			_, _ = w.Write([]byte(`{"reads":[{"startTime":"2026-01-01T00:00:00-08:00","endTime":"2026-01-01T01:00:00-08:00","consumption":{"value":2}}]}`))
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	// The redirect is returned by the login action and must be followed with cookies.
	o := &Opower{Client: Client{BaseURL: srv.URL, HTTP: &http.Client{Jar: jar}}, LoginURL: srv.URL, Username: "user", Password: "password", Utility: "Pacific Gas and Electric Company (PG&E)", Now: func() time.Time { return time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC) }}
	if err := o.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	accounts, err := o.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].ID != "acct" || accounts[0].UUID != "acct-uuid" {
		t.Fatalf("accounts=%+v", accounts)
	}
	reads, err := o.Intervals(context.Background(), "acct-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if len(reads) != 1 || reads[0].Consumption != 2 {
		t.Fatalf("reads=%+v", reads)
	}
	if !(sawLogin && sawRedirect && sawHome && sawAura && sawBrowser && sawCustomers && sawReads) {
		t.Fatalf("request shape login=%v redirect=%v home=%v aura=%v browser=%v customers=%v reads=%v", sawLogin, sawRedirect, sawHome, sawAura, sawBrowser, sawCustomers, sawReads)
	}
}

func TestOpowerStartMFAUsesExpectedAuraChallengeShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		_ = json.Unmarshal([]byte(form.Get("message")), &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"actions":[{"state":"SUCCESS","returnValue":{"returnValue":{"options":[{"label":"Email","value":"Email"},{"label":"Phone","value":"Phone"}]}}}]}`))
	}))
	defer srv.Close()
	o := &Opower{Client: Client{BaseURL: srv.URL, HTTP: srv.Client()}, LoginURL: srv.URL}
	options, err := o.StartMFA(context.Background())
	if err != nil || len(options) != 2 || options[0].Label != "Email" {
		t.Fatalf("options=%+v err=%v", options, err)
	}
	actions := got["actions"].([]any)
	params := actions[0].(map[string]any)["params"].(map[string]any)
	if params["classname"] != "MfaChallenge" || params["method"] != "async_get_mfa_options" {
		t.Fatalf("unexpected action=%v", params)
	}
}

func TestOpowerSelectVerifyMFAExactShapeAndProtectedState(t *testing.T) {
	dir := t.TempDir()
	var selectParams, verifyParams map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		var msg map[string]any
		_ = json.Unmarshal([]byte(form.Get("message")), &msg)
		action := msg["actions"].([]any)[0].(map[string]any)["params"].(map[string]any)
		if action["method"] == "handleChoiceofMFA" {
			selectParams = action["params"].(map[string]any)
		} else {
			verifyParams = action["params"].(map[string]any)["input"].(map[string]any)
		}
		w.Header().Set("Content-Type", "application/json")
		if action["method"] == "verifySignInCode" {
			_, _ = w.Write([]byte(`{"actions":[{"state":"SUCCESS","returnValue":{"returnValue":{"returnResponse":"success","wrapperObj":{"retencrUsrname":"browser-state","encryptedKey":"validation-state","expiryDateTime":"tomorrow"}}}}]}`))
		} else {
			_, _ = w.Write([]byte(`{"actions":[{"state":"SUCCESS"}]}`))
		}
	}))
	defer srv.Close()
	o := &Opower{Client: Client{BaseURL: srv.URL, HTTP: srv.Client(), Credentials: "token"}, LoginURL: srv.URL, Username: "user", Password: "password", MFAStatePath: dir + "/pge-login.json", LoginData: map[string]string{"encryptedTFT": "enc"}}
	if err := o.SelectMFA(context.Background(), "Phone"); err != nil {
		t.Fatal(err)
	}
	if err := o.VerifyMFA(context.Background(), "123456"); err != nil {
		t.Fatal(err)
	}
	if selectParams["username"] != "user" || selectParams["selectedChoice"] != "Phone" || selectParams["isforgotpassword"] != false {
		t.Fatalf("select=%v", selectParams)
	}
	if verifyParams["authCode"] != "123456" || verifyParams["password"] != "password" || verifyParams["encToken"] != "enc" || verifyParams["otpType"] != "Phone" || verifyParams["isForgotPasswordFlow"] != false {
		t.Fatalf("verify=%v", verifyParams)
	}
	info, err := os.Stat(o.MFAStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	data, _ := os.ReadFile(o.MFAStatePath)
	if strings.Contains(string(data), "123456") {
		t.Fatal("MFA code persisted")
	}
}

func TestOpowerCollectResumesPersistedSession(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"
	data, _ := json.Marshal(mfaState{Credentials: "resumed-token"})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "customers") {
			_, _ = w.Write([]byte(`{"customers":[{"uuid":"customer","utilityAccounts":[{"uuid":"acct","preferredUtilityAccountId":"acct","meterType":"ELECTRIC","readResolution":"DAY"}]}]}`))
			return
		}
		if !strings.Contains(r.URL.Path, "reads") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer resumed-token" {
			t.Fatalf("authorization leaked/wrong")
		}
		_, _ = w.Write([]byte(`{"reads":[{"startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T01:00:00Z","consumption":{"value":2}}]}`))
	}))
	defer srv.Close()
	o := &Opower{Client: Client{BaseURL: srv.URL, HTTP: srv.Client()}, LoginURL: srv.URL, MFAStatePath: path, Now: func() time.Time { return time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC) }}
	rs, err := o.Collect(context.Background(), domain.Setup{Name: "home", Provider: "pge", AccountID: "acct"})
	if err != nil || len(rs) != 1 {
		t.Fatalf("readings=%+v err=%v", rs, err)
	}
}

func TestOpowerMFARejectsInvalidInputsAndMissingSession(t *testing.T) {
	o := &Opower{}
	if err := o.SelectMFA(context.Background(), "totp"); err == nil {
		t.Fatal("invalid option accepted")
	}
	if err := o.VerifyMFA(context.Background(), "abc"); err == nil {
		t.Fatal("invalid code accepted")
	}
	if _, err := o.StartMFA(context.Background()); err == nil {
		t.Fatal("missing session accepted")
	}
}

func TestOpowerMFAIsClassifiedWithoutUnsafeContinuation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"actions":[{"state":"SUCCESS","returnValue":{"returnValue":{"retMessage":"verifymfa :","EmailVal":"u***@example.com","PhoneVal":"***1234"}}}]}`))
	}))
	defer srv.Close()
	o := &Opower{Client: Client{BaseURL: srv.URL, HTTP: srv.Client()}, LoginURL: srv.URL, Username: "user", Password: "password"}
	err := o.Login(context.Background())
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Class != ErrMFARequired {
		t.Fatalf("error=%T %v", err, err)
	}
	if o.Credentials != "" {
		t.Fatal("MFA login must not expose a token")
	}
}

func TestOpowerCollectConvertsKWhAndUsesSetup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "customers") {
			_, _ = w.Write([]byte(`{"customers":[{"uuid":"customer","utilityAccounts":[{"uuid":"uuid","preferredUtilityAccountId":"acct","meterType":"ELECTRIC","readResolution":"DAY"}]}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "reads") {
			_, _ = w.Write([]byte(`{"reads":[{"startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:15:00Z","consumption":{"value":1.25}}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	o := &Opower{Client: Client{BaseURL: srv.URL, HTTP: srv.Client(), Credentials: "token"}, Now: func() time.Time { return time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC) }}
	rs, err := o.Collect(context.Background(), domain.Setup{Name: "home", Provider: "pge", AccountID: "uuid"})
	if err != nil || len(rs) != 1 || rs[0].Setup != "home" || rs[0].KWh != 1.25 || rs[0].Watts != 5000 {
		t.Fatalf("readings=%+v err=%v", rs, err)
	}
}

func TestOpowerCollectsEveryDiscoveredAccount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "customers") {
			_, _ = w.Write([]byte(`{"customers":[{"uuid":"customer","utilityAccounts":[{"uuid":"first","preferredUtilityAccountId":"first"},{"uuid":"second","preferredUtilityAccountId":"second"}]}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "reads") {
			_, _ = w.Write([]byte(`{"reads":[{"startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T01:00:00Z","consumption":{"value":1}}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	o := &Opower{Client: Client{BaseURL: srv.URL, HTTP: srv.Client(), Credentials: "token"}}
	rs, err := o.Collect(context.Background(), domain.Setup{Name: "home", Provider: "pge"})
	if err != nil || len(rs) != 2 || rs[0].Identity == rs[1].Identity || rs[0].Channel != "net_energy" {
		t.Fatalf("readings=%+v err=%v", rs, err)
	}
}
