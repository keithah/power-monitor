package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
)

type ErrorClass string

const (
	ErrMissingCredential ErrorClass = "missing_credentials"
	ErrAuthentication    ErrorClass = "authentication"
	ErrMFARequired       ErrorClass = "mfa_required"
	ErrUpstream          ErrorClass = "upstream"
	ErrUnavailable       ErrorClass = "unavailable"
)

type ProviderError struct {
	Class ErrorClass
	Err   error
}

func (e *ProviderError) Error() string { return string(e.Class) + ": " + e.Err.Error() }
func (e *ProviderError) Unwrap() error { return e.Err }
func perr(c ErrorClass, e error) error { return &ProviderError{c, e} }

type Provider interface {
	Collect(context.Context, domain.Setup) ([]domain.Reading, error)
}
type Mock struct {
	Readings []domain.Reading
	Err      error
}

func (m Mock) Collect(context.Context, domain.Setup) ([]domain.Reading, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return m.Readings, nil
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}
type Client struct {
	BaseURL     string
	HTTP        HTTPDoer
	Credentials string
	AuthHeader  string
}

func (c Client) requestURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimRight(c.BaseURL, "/") + path
}
func (c Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	return c.doRequest(ctx, method, path, rd, body != nil, out)
}
func (c Client) doRequest(ctx context.Context, method, path string, rd io.Reader, jsonBody bool, out any) error {
	if c.HTTP == nil {
		c.HTTP = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, method, c.requestURL(path), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if jsonBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Credentials != "" {
		if c.AuthHeader != "" {
			req.Header.Set(c.AuthHeader, c.Credentials)
			// Emporia deployments have used both header names; current API
			// authorization accepts the standard bearer form while older
			// clients expect authtoken. Sending both is compatibility-safe.
			if strings.EqualFold(c.AuthHeader, "authtoken") {
				req.Header.Set("Authorization", "Bearer "+c.Credentials)
			}
		} else {
			req.Header.Set("Authorization", "Bearer "+c.Credentials)
		}
	}
	return c.readResponse(req, out)
}
func (c Client) readResponse(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return perr(ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return perr(ErrAuthentication, errors.New("provider rejected credentials"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return perr(ErrUpstream, fmt.Errorf("provider returned HTTP %d", resp.StatusCode))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return perr(ErrUpstream, errors.New("invalid provider response"))
		}
	}
	return nil
}
func (c Client) doForm(ctx context.Context, path string, values url.Values, out any) error {
	if c.HTTP == nil {
		c.HTTP = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.requestURL(path), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.readResponse(req, out)
}

type Enphase struct {
	Client
	Username, Password string
	SiteIDs            []string
}

func (e *Enphase) Login(ctx context.Context) error {
	if e.Username == "" || e.Password == "" {
		return perr(ErrMissingCredential, errors.New("Enphase credentials not configured"))
	}
	if e.HTTP == nil {
		e.HTTP = &http.Client{}
	}
	if hc, ok := e.HTTP.(*http.Client); ok && hc.Jar == nil {
		hc.Jar, _ = cookiejar.New(nil)
	}
	return e.doForm(ctx, "/login/login.json", url.Values{"user[email]": {e.Username}, "user[password]": {e.Password}}, nil)
}
func (e Enphase) Sites(ctx context.Context) ([]map[string]any, error) {
	var raw json.RawMessage
	if err := e.do(ctx, http.MethodGet, "/app-api/user_sites.json", nil, &raw); err != nil {
		return nil, err
	}
	var wrapped struct {
		Sites []map[string]any `json:"sites"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Sites != nil {
		return wrapped.Sites, nil
	}
	var sites []map[string]any
	if err := json.Unmarshal(raw, &sites); err != nil {
		return nil, perr(ErrUpstream, errors.New("invalid Enphase sites response"))
	}
	return sites, nil
}
func (e Enphase) Production(ctx context.Context, site string) (domain.Reading, error) {
	if site == "" {
		return domain.Reading{}, perr(ErrUpstream, errors.New("Enphase site ID is empty"))
	}
	var v struct {
		Stats []struct {
			Production []float64 `json:"production"`
			Totals     struct {
				Production float64 `json:"production"`
			} `json:"totals"`
		} `json:"stats"`
		Totals struct {
			Production float64 `json:"production"`
		} `json:"totals"`
	}
	if err := e.do(ctx, http.MethodGet, "/pv/systems/"+url.PathEscape(site)+"/today", nil, &v); err != nil {
		return domain.Reading{}, err
	}
	if len(v.Stats) == 0 {
		return domain.Reading{}, perr(ErrUpstream, errors.New("site has no production data"))
	}
	p := v.Stats[0]
	watts := 0.0
	if len(p.Production) > 0 {
		watts = p.Production[len(p.Production)-1]
	}
	kwh := p.Totals.Production
	if kwh == 0 {
		kwh = v.Totals.Production
	}
	return domain.Reading{Provider: "enphase", Identity: site, Channel: "production", Role: domain.Generation, Timestamp: time.Now().UTC(), Watts: watts, KWh: kwh / 1000, Unit: "kWh"}, nil
}
func (e Enphase) Collect(ctx context.Context, s domain.Setup) ([]domain.Reading, error) {
	if e.Username != "" || e.Password != "" {
		if err := e.Login(ctx); err != nil {
			return nil, err
		}
	}
	ids := append([]string(nil), e.SiteIDs...)
	if len(ids) == 0 {
		ids = splitIDs(s.SiteID)
	}
	if len(ids) == 0 {
		sites, err := e.Sites(ctx)
		if err != nil {
			return nil, err
		}
		for _, site := range sites {
			for _, key := range []string{"site_id", "system_id", "id"} {
				if x, ok := site[key]; ok {
					ids = append(ids, fmt.Sprint(x))
					break
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil, perr(ErrUpstream, errors.New("no Enphase sites configured or discovered"))
	}
	out := make([]domain.Reading, 0, len(ids))
	for _, id := range ids {
		r, err := e.Production(ctx, id)
		if err != nil {
			return nil, err
		}
		r.Setup = s.Name
		out = append(out, r)
	}
	return out, nil
}

func splitIDs(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

type Emporia struct {
	Client
	Email, Password, CognitoURL, ClientID string
}
type EmporiaDevice struct {
	GID         any              `json:"device_gid"`
	DeviceID    string           `json:"device_id"`
	DisplayName string           `json:"display_name"`
	Model       string           `json:"model"`
	Timezone    string           `json:"time_zone"`
	Channels    []EmporiaChannel `json:"channels"`
}
type EmporiaChannel struct {
	ID             string `json:"channel_id"`
	Classification string `json:"channel_classification"`
	HasData        bool   `json:"has_data"`
}

func (e *Emporia) Authenticate(ctx context.Context) error {
	if e.Email == "" || e.Password == "" {
		return perr(ErrMissingCredential, errors.New("Emporia credentials not configured"))
	}
	endpoint := e.CognitoURL
	if endpoint == "" {
		endpoint = "https://cognito-idp.us-east-2.amazonaws.com"
	}
	clientID := e.ClientID
	if clientID == "" {
		clientID = "4qte47jbstod8apnfic0bunmrq"
	}
	body, _ := json.Marshal(map[string]any{"AuthFlow": "USER_PASSWORD_AUTH", "ClientId": clientID, "AuthParameters": map[string]string{"USERNAME": e.Email, "PASSWORD": e.Password}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService.InitiateAuth")
	if e.HTTP == nil {
		e.HTTP = http.DefaultClient
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return perr(ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 {
		return perr(ErrAuthentication, errors.New("Emporia authentication rejected"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return perr(ErrUpstream, fmt.Errorf("Cognito returned HTTP %d", resp.StatusCode))
	}
	var v struct {
		AuthenticationResult struct {
			IDToken string `json:"IdToken"`
		} `json:"AuthenticationResult"`
	}
	if json.Unmarshal(data, &v) != nil || v.AuthenticationResult.IDToken == "" {
		return perr(ErrAuthentication, errors.New("Cognito response did not contain an id token"))
	}
	e.Credentials = v.AuthenticationResult.IDToken
	e.AuthHeader = "authtoken"
	return nil
}
func (e *Emporia) ensureAuth(ctx context.Context) error {
	if e.Credentials != "" {
		e.AuthHeader = "authtoken"
		return nil
	}
	return e.Authenticate(ctx)
}
func (e *Emporia) Devices(ctx context.Context) ([]EmporiaDevice, error) {
	if err := e.ensureAuth(ctx); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := e.do(ctx, http.MethodGet, "/v1/customers/devices", nil, &raw); err != nil {
		return nil, err
	}
	var v struct {
		Devices []EmporiaDevice `json:"devices"`
	}
	if json.Unmarshal(raw, &v) == nil && v.Devices != nil {
		return v.Devices, nil
	}
	var devices []EmporiaDevice
	if err := json.Unmarshal(raw, &devices); err != nil {
		return nil, perr(ErrUpstream, errors.New("invalid Emporia devices response"))
	}
	return devices, nil
}
func (e *Emporia) Usages(ctx context.Context, gid any) ([]domain.Reading, error) {
	if err := e.ensureAuth(ctx); err != nil {
		return nil, err
	}
	u := url.Values{"device_gids": {fmt.Sprint(gid)}, "instant": {time.Now().UTC().Format(time.RFC3339)}, "scale": {"HOUR"}, "energy_unit": {"KILOWATT_HOURS"}}
	path := "/v1/customers/devices/usages?" + u.Encode()
	var v struct {
		Instant      string `json:"instant"`
		DeviceUsages []struct {
			ChannelUsages []struct {
				ID    string  `json:"channel_id"`
				Usage float64 `json:"usage"`
			} `json:"channel_usages"`
		} `json:"device_usages"`
	}
	if err := e.do(ctx, http.MethodGet, path, nil, &v); err != nil {
		return nil, err
	}
	ts := time.Now().UTC()
	if v.Instant != "" {
		if x, err := time.Parse(time.RFC3339, v.Instant); err == nil {
			ts = x.UTC()
		}
	}
	start := ts.Truncate(time.Hour)
	end := start.Add(time.Hour)
	out := []domain.Reading{}
	for _, d := range v.DeviceUsages {
		for _, ch := range d.ChannelUsages {
			if ch.ID == "TotalUsage" || ch.ID == "Balance" || !(ch.ID == "Mains" || strings.HasPrefix(ch.ID, "Branch_")) {
				continue
			}
			role := domain.Branch
			if ch.ID == "Mains" {
				role = domain.Mains
			}
			out = append(out, domain.Reading{Provider: "emporia", Identity: fmt.Sprint(gid), Channel: ch.ID, Role: role, Timestamp: start, WindowStart: start, WindowEnd: end, KWh: ch.Usage, Unit: "kWh"})
		}
	}
	return out, nil
}
func (e *Emporia) Collect(ctx context.Context, s domain.Setup) ([]domain.Reading, error) {
	rs, err := e.Usages(ctx, s.DeviceGID)
	if err != nil {
		return nil, err
	}
	for i := range rs {
		rs[i].Setup = s.Name
		if s.Role != "" && rs[i].Role == domain.Mains {
			rs[i].Role = s.Role
		}
	}
	return rs, nil
}

// Opower implements the PG&E portal login and its Opower DataBrowser API.
// MFA is deliberately surfaced as ErrMFARequired; continuation is not attempted
// because it requires carrying interactive Aura state across a caller boundary.
type MFAOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type MFASession interface {
	StartMFA(context.Context) ([]MFAOption, error)
	SelectMFA(context.Context, string) error
	VerifyMFA(context.Context, string) error
}

type mfaState struct {
	BrowserCookie    string         `json:"browsercookie,omitempty"`
	ValidationCookie string         `json:"validationCookie,omitempty"`
	ExpiryDateTime   string         `json:"expiryDateTime,omitempty"`
	Credentials      string         `json:"credentials,omitempty"`
	Cookies          []*http.Cookie `json:"cookies,omitempty"`
}

type Opower struct {
	Client
	Username, Password, Utility string
	LoginURL                    string
	LoginData                   map[string]string
	MFA                         MFASession
	MFAStatePath                string
	mfaReady                    bool
	Now                         func() time.Time
}
type OpowerAccount struct {
	ID, UUID, CustomerUUID, MeterType string
	Name                              string
	ReadResolution                    string
}
type OpowerInterval struct {
	Start, End                  string
	Consumption, Import, Export float64
}

func (o *Opower) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}
func (o *Opower) loginBase() string {
	if o.LoginURL != "" {
		return strings.TrimRight(o.LoginURL, "/")
	}
	return "https://myaccount.pge.com"
}
func (o *Opower) aura(ctx context.Context, body map[string]any, out any) error {
	b, _ := json.Marshal(body["message"])
	form := url.Values{"message": {string(b)}, "aura.context": {jsonString(body["aura.context"])}, "aura.pageURI": {fmt.Sprint(body["aura.pageURI"])}, "aura.token": {fmt.Sprint(body["aura.token"])}}
	return o.doForm(ctx, o.loginBase()+"/myaccount/s/sfsites/aura?aura.ApexAction.execute=1", form, out)
}
func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }
func (o *Opower) mfaPath() string {
	if o.MFAStatePath != "" {
		return o.MFAStatePath
	}
	if p := os.Getenv("POWER_MONITOR_PGE_LOGIN_PATH"); p != "" {
		return p
	}
	return "/data/pge-login.json"
}
func (o *Opower) mfaSession() MFASession {
	if o.MFA != nil {
		return o.MFA
	}
	return o
}
func (o *Opower) mfaAction(ctx context.Context, method string, params map[string]any, out any) error {
	classname := "MyAcct_Apex_CustomMFAController"
	if method == "async_get_mfa_options" {
		classname = "MfaChallenge"
	}
	body := map[string]any{"message": map[string]any{"actions": []any{map[string]any{
		"descriptor": "aura://ApexActionController/ACTION$execute",
		"params":     map[string]any{"classname": classname, "method": method, "params": params},
	}}}, "aura.context": map[string]string{"app": "siteforce:loginApp2"}, "aura.pageURI": "/myaccount/s/login/", "aura.token": "null"}
	return o.aura(ctx, body, out)
}
func (o *Opower) StartMFA(ctx context.Context) ([]MFAOption, error) {
	if o.MFA != nil {
		return o.MFA.StartMFA(ctx)
	}
	if !o.mfaReady && o.LoginData["retencrUsrname"] == "" && o.Username != "" {
		if err := o.Login(ctx); err != nil {
			var pe *ProviderError
			if !errors.As(err, &pe) || pe.Class != ErrMFARequired {
				return nil, err
			}
		}
	}
	var res struct {
		Actions []struct {
			State       string `json:"state"`
			ReturnValue struct {
				ReturnValue struct {
					EmailVal string      `json:"EmailVal"`
					PhoneVal string      `json:"PhoneVal"`
					Options  []MFAOption `json:"options"`
				} `json:"returnValue"`
			} `json:"returnValue"`
		} `json:"actions"`
	}
	if err := o.mfaAction(ctx, "async_get_mfa_options", nil, &res); err != nil {
		return nil, err
	}
	if len(res.Actions) == 0 || res.Actions[0].State != "SUCCESS" {
		return nil, perr(ErrMFARequired, errors.New("PG&E MFA options unavailable"))
	}
	rv := res.Actions[0].ReturnValue.ReturnValue
	options := rv.Options
	if len(options) == 0 {
		if rv.EmailVal != "" {
			options = append(options, MFAOption{Label: "Email", Value: "Email"})
		}
		if rv.PhoneVal != "" {
			options = append(options, MFAOption{Label: "Phone", Value: "Phone"})
		}
	}
	masked := make([]MFAOption, 0, len(options))
	for _, option := range options {
		label := strings.Title(strings.ToLower(strings.TrimSpace(option.Label)))
		if label != "Email" && label != "Phone" {
			label = strings.Title(strings.ToLower(strings.TrimSpace(option.Value)))
		}
		if label == "Email" || label == "Phone" {
			masked = append(masked, MFAOption{Label: label, Value: label})
		}
	}
	if len(masked) == 0 {
		return nil, perr(ErrMFARequired, errors.New("PG&E MFA returned no delivery options"))
	}
	o.mfaReady = true
	return masked, nil
}
func validMFAOption(option string) bool {
	x := strings.ToLower(strings.TrimSpace(option))
	return x == "email" || x == "phone"
}
func (o *Opower) SelectMFA(ctx context.Context, option string) error {
	if !validMFAOption(option) {
		return perr(ErrMFARequired, errors.New("MFA option must be Email or Phone"))
	}
	if o.MFA != nil {
		return o.MFA.SelectMFA(ctx, option)
	}
	if o.Username == "" {
		return perr(ErrMissingCredential, errors.New("PG&E username is required for MFA"))
	}
	var res struct {
		Actions []struct {
			State string `json:"state"`
		} `json:"actions"`
	}
	params := map[string]any{"username": firstNonEmpty(o.LoginData["retencrUsrname"], o.Username), "selectedChoice": strings.Title(strings.ToLower(strings.TrimSpace(option))), "isforgotpassword": false}
	if err := o.mfaAction(ctx, "handleChoiceofMFA", params, &res); err != nil {
		return err
	}
	if len(res.Actions) == 0 || res.Actions[0].State != "SUCCESS" {
		return perr(ErrMFARequired, errors.New("PG&E MFA option was rejected"))
	}
	if o.LoginData == nil {
		o.LoginData = map[string]string{}
	}
	o.LoginData["selectedChoice"] = params["selectedChoice"].(string)
	return nil
}
func (o *Opower) VerifyMFA(ctx context.Context, code string) error {
	if o.MFA != nil {
		return o.MFA.VerifyMFA(ctx, code)
	}
	code = strings.TrimSpace(code)
	if len(code) < 4 || len(code) > 8 {
		return perr(ErrMFARequired, errors.New("MFA code is invalid"))
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return perr(ErrMFARequired, errors.New("MFA code is invalid"))
		}
	}
	if o.Username == "" {
		return perr(ErrMissingCredential, errors.New("PG&E username is required for MFA"))
	}
	var res struct {
		Actions []struct {
			State       string `json:"state"`
			ReturnValue struct {
				ReturnValue struct {
					ReturnResponse string `json:"returnResponse"`
					WrapperObj     struct {
						RetencrUsrname string `json:"retencrUsrname"`
						EncryptedKey   string `json:"encryptedKey"`
						ExpiryDateTime string `json:"expiryDateTime"`
					} `json:"wrapperObj"`
				} `json:"returnValue"`
				ReturnResponse string `json:"returnResponse"`
				WrapperObj     struct {
					RetencrUsrname string `json:"retencrUsrname"`
					EncryptedKey   string `json:"encryptedKey"`
					ExpiryDateTime string `json:"expiryDateTime"`
				} `json:"wrapperObj"`
			} `json:"returnValue"`
		} `json:"actions"`
	}
	params := map[string]any{"authCode": code, "password": o.Password, "encToken": o.LoginData["encryptedTFT"], "usernameVal": firstNonEmpty(o.LoginData["retencrUsrname"], o.Username), "isForgotPasswordFlow": false, "otpType": firstNonEmpty(o.LoginData["selectedChoice"], "Email")}
	if err := o.mfaAction(ctx, "verifySignInCode", map[string]any{"input": params}, &res); err != nil {
		return err
	}
	if len(res.Actions) == 0 || res.Actions[0].State != "SUCCESS" {
		return perr(ErrAuthentication, errors.New("PG&E MFA verification failed"))
	}
	rv := res.Actions[0].ReturnValue.ReturnValue
	if rv.ReturnResponse == "" {
		rv.ReturnResponse = res.Actions[0].ReturnValue.ReturnResponse
	}
	if rv.WrapperObj.RetencrUsrname == "" {
		rv.WrapperObj = res.Actions[0].ReturnValue.WrapperObj
	}
	if !strings.EqualFold(rv.ReturnResponse, "success") || rv.WrapperObj.RetencrUsrname == "" || rv.WrapperObj.EncryptedKey == "" {
		return perr(ErrAuthentication, errors.New("PG&E MFA response contained no usable session state"))
	}
	if o.LoginData == nil {
		o.LoginData = map[string]string{}
	}
	o.LoginData["browsercookie"], o.LoginData["validationCookie"], o.LoginData["expiryDateTime"] = rv.WrapperObj.RetencrUsrname, rv.WrapperObj.EncryptedKey, rv.WrapperObj.ExpiryDateTime
	return o.persistMFAState()
}
func (o *Opower) persistMFAState() error {
	if o.LoginData == nil || o.LoginData["browsercookie"] == "" || o.LoginData["validationCookie"] == "" {
		return perr(ErrAuthentication, errors.New("PG&E MFA produced no usable session state"))
	}
	state := mfaState{BrowserCookie: o.LoginData["browsercookie"], ValidationCookie: o.LoginData["validationCookie"], ExpiryDateTime: o.LoginData["expiryDateTime"], Credentials: o.Credentials}
	if hc, ok := o.HTTP.(*http.Client); ok && hc.Jar != nil {
		state.Cookies = hc.Jar.Cookies(mustURL(o.loginBase()))
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(o.mfaPath(), data, 0600); err != nil {
		return perr(ErrUnavailable, err)
	}
	return os.Chmod(o.mfaPath(), 0600)
}
func (o *Opower) loadMFAState() error {
	data, err := os.ReadFile(o.mfaPath())
	if err != nil {
		return err
	}
	var state mfaState
	if json.Unmarshal(data, &state) != nil || (state.Credentials == "" && (state.BrowserCookie == "" || state.ValidationCookie == "")) {
		return errors.New("PG&E persisted session is unusable")
	}
	o.Credentials = state.Credentials
	o.LoginData = map[string]string{"browsercookie": state.BrowserCookie, "validationCookie": state.ValidationCookie, "expiryDateTime": state.ExpiryDateTime}
	if hc, ok := o.HTTP.(*http.Client); ok {
		if hc.Jar == nil {
			hc.Jar, _ = cookiejar.New(nil)
		}
		hc.Jar.SetCookies(mustURL(o.loginBase()), state.Cookies)
	}
	return nil
}
func (o *Opower) Login(ctx context.Context) error {
	if o.Username == "" || o.Password == "" {
		return perr(ErrMissingCredential, errors.New("PG&E credentials not configured"))
	}
	if hc, ok := o.HTTP.(*http.Client); ok && hc.Jar == nil {
		hc.Jar, _ = cookiejar.New(nil)
	}
	login := map[string]any{"message": map[string]any{"actions": []any{map[string]any{"descriptor": "aura://ApexActionController/ACTION$execute", "params": map[string]any{"classname": "MyAcct_customLoginLWCController", "method": "login", "params": map[string]any{"username": o.Username, "password": o.Password, "browsercookie": firstNonEmpty(o.LoginData["browsercookie"], "null"), "validationCookie": firstNonEmpty(o.LoginData["validationCookie"], "null")}}}}}, "aura.context": map[string]string{"app": "siteforce:loginApp2"}, "aura.pageURI": "/myaccount/s/login/", "aura.token": "null"}
	var res struct {
		Actions []struct {
			State       string `json:"state"`
			ReturnValue struct {
				ReturnValue struct {
					RetMessage     string `json:"retMessage"`
					RetencrUsrname string `json:"retencrUsrname"`
					EncryptedTFT   string `json:"encryptedTFT"`
				} `json:"returnValue"`
			} `json:"returnValue"`
		} `json:"actions"`
	}
	if err := o.aura(ctx, login, &res); err != nil {
		return err
	}
	if len(res.Actions) == 0 {
		return perr(ErrAuthentication, errors.New("PG&E login returned no actions"))
	}
	rv := res.Actions[0].ReturnValue.ReturnValue
	if strings.EqualFold(rv.RetMessage, "verifymfa :") {
		if o.LoginData == nil {
			o.LoginData = map[string]string{}
		}
		if rv.RetencrUsrname != "" {
			o.LoginData["retencrUsrname"] = rv.RetencrUsrname
		}
		if rv.EncryptedTFT != "" {
			o.LoginData["encryptedTFT"] = rv.EncryptedTFT
		}
		o.mfaReady = true
		return perr(ErrMFARequired, errors.New("PG&E MFA is required; use StartMFA, SelectMFA, and VerifyMFA"))
	}
	if res.Actions[0].State != "SUCCESS" || !strings.HasPrefix(rv.RetMessage, "http") {
		return perr(ErrAuthentication, errors.New("PG&E login rejected credentials"))
	}
	if err := o.doRequest(ctx, http.MethodGet, rv.RetMessage, nil, false, nil); err != nil {
		return err
	}
	if err := o.doRequest(ctx, http.MethodGet, o.loginBase()+"/myaccount/s/", nil, false, nil); err != nil {
		return err
	}
	var token string
	if hc, ok := o.HTTP.(*http.Client); ok && hc.Jar != nil {
		for _, c := range hc.Jar.Cookies(mustURL(o.loginBase())) {
			if strings.HasPrefix(c.Name, "__Host-ERIC_PROD") {
				token = c.Value
				break
			}
		}
	}
	if token == "" {
		return perr(ErrAuthentication, errors.New("PG&E login returned no Aura session cookie"))
	}
	for _, action := range []struct{ class, method string }{{"MyAcct_OneTrustIntegrationController", "generateToken"}, {"MyAcct_AccountCacheHandler", "copyToSessionCacheForUser"}} {
		body := map[string]any{"message": map[string]any{"actions": []any{map[string]any{"descriptor": "aura://ApexActionController/ACTION$execute", "params": map[string]any{"classname": action.class, "method": action.method}}}}, "aura.context": map[string]string{"app": "siteforce:communityApp"}, "aura.pageURI": "/myaccount/s/", "aura.token": token}
		if err := o.aura(ctx, body, nil); err != nil {
			return err
		}
	}
	var page string
	if err := o.text(ctx, o.loginBase()+"/myaccount/apex/MyAcct_VF_BillInsights_OpowerDataBrowser", &page); err != nil {
		return err
	}
	start := strings.Index(page, "let tokenFromApex = '")
	if start < 0 {
		return perr(ErrAuthentication, errors.New("PG&E page returned no Opower token"))
	}
	start += len("let tokenFromApex = '")
	end := strings.Index(page[start:], "'")
	if end <= 0 {
		return perr(ErrAuthentication, errors.New("PG&E page returned empty Opower token"))
	}
	o.Credentials = page[start : start+end]
	o.AuthHeader = ""
	return nil
}
func mustURL(raw string) *url.URL { u, _ := url.Parse(raw); return u }
func (o *Opower) text(ctx context.Context, raw string, out *string) error {
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return e
	}
	resp, e := o.HTTP.Do(req)
	if e != nil {
		return perr(ErrUnavailable, e)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return perr(ErrUpstream, fmt.Errorf("provider returned HTTP %d", resp.StatusCode))
	}
	*out = string(b)
	return nil
}
func (o *Opower) utilityCode() string { return "pge" }
func (o *Opower) Accounts(ctx context.Context) ([]OpowerAccount, error) {
	var v struct {
		Customers []struct {
			UUID            string `json:"uuid"`
			UtilityAccounts []struct {
				UUID       string `json:"uuid"`
				ID         string `json:"preferredUtilityAccountId"`
				Name       string `json:"name"`
				MeterType  string `json:"meterType"`
				Resolution string `json:"readResolution"`
			} `json:"utilityAccounts"`
		} `json:"customers"`
	}
	path := "/ei/edge/apis/multi-account-v1/cws/" + o.utilityCode() + "/customers?offset=0&batchSize=100&addressFilter="
	if err := o.do(ctx, http.MethodGet, path, nil, &v); err != nil {
		return nil, err
	}
	out := []OpowerAccount{}
	counts := map[string]int{}
	for _, c := range v.Customers {
		for _, a := range c.UtilityAccounts {
			counts[a.ID]++
			out = append(out, OpowerAccount{ID: a.ID, UUID: a.UUID, CustomerUUID: c.UUID, Name: a.Name, MeterType: a.MeterType, ReadResolution: a.Resolution})
		}
	}
	for i := range out {
		if counts[out[i].ID] > 1 {
			out[i].ID = out[i].UUID
		}
	}
	return out, nil
}
func (o *Opower) Intervals(ctx context.Context, account string) ([]OpowerInterval, error) {
	now := o.now()
	u := url.Values{"aggregateType": {"day"}, "startDate": {now.Add(-48 * time.Hour).Format("2006-01-02")}, "endDate": {now.Format("2006-01-02")}}
	var v struct {
		Reads []struct {
			Start       string `json:"startTime"`
			End         string `json:"endTime"`
			Consumption struct {
				Value float64 `json:"value"`
			} `json:"consumption"`
		} `json:"reads"`
	}
	path := "/ei/edge/apis/DataBrowser-v1/cws/utilities/" + o.utilityCode() + "/utilityAccounts/" + url.PathEscape(account) + "/reads?" + u.Encode()
	if err := o.do(ctx, http.MethodGet, path, nil, &v); err != nil {
		return nil, err
	}
	out := make([]OpowerInterval, len(v.Reads))
	for i, r := range v.Reads {
		out[i] = OpowerInterval{Start: r.Start, End: r.End, Consumption: r.Consumption.Value}
	}
	return out, nil
}
func (o *Opower) Collect(ctx context.Context, s domain.Setup) ([]domain.Reading, error) {
	if o.Credentials == "" {
		_ = o.loadMFAState()
	}
	if o.Credentials == "" {
		if err := o.Login(ctx); err != nil {
			return nil, err
		}
	}
	accounts, e := o.Accounts(ctx)
	if e != nil || len(accounts) == 0 {
		return nil, perr(ErrUpstream, errors.New("no PG&E accounts discovered"))
	}
	selected := []OpowerAccount{}
	for _, account := range accounts {
		if s.AccountID != "" && s.AccountID != account.UUID && s.AccountID != account.ID {
			continue
		}
		selected = append(selected, account)
	}
	if len(selected) == 0 {
		return nil, perr(ErrUpstream, errors.New("no PG&E accounts selected"))
	}
	out := []domain.Reading{}
	for _, account := range selected {
		iv, e := o.Intervals(ctx, account.UUID)
		if e != nil {
			return nil, e
		}
		for _, x := range iv {
			end, e := time.Parse(time.RFC3339, x.End)
			if e != nil {
				return nil, perr(ErrUpstream, errors.New("invalid Opower interval timestamp"))
			}
			start, se := time.Parse(time.RFC3339, x.Start)
			watts := 0.0
			if se == nil && end.After(start) {
				watts = x.Consumption * 3600 / end.Sub(start).Seconds() * 1000
			}
			out = append(out, domain.Reading{Provider: "pge", Setup: s.Name, Identity: account.UUID, Channel: "net_energy", Role: domain.Utility, Timestamp: end, WindowStart: start, WindowEnd: end, KWh: x.Consumption, Watts: watts, Unit: "kWh"})
		}
	}
	return out, nil
}

func For(s domain.Setup) (Provider, error) {
	switch strings.ToLower(s.Provider) {
	case "enphase":
		return &Enphase{}, nil
	case "emporia":
		return &Emporia{}, nil
	case "opower", "pge":
		return &Opower{}, nil
	}
	return nil, fmt.Errorf("unsupported provider %q", s.Provider)
}
func Configured(s domain.Setup) (Provider, error) {
	secret, err := credentialFor(s)
	if err != nil {
		return nil, err
	}
	base := os.Getenv("POWER_MONITOR_" + strings.ToUpper(s.Provider) + "_BASE_URL")
	if base == "" {
		base = os.Getenv("POWER_MONITOR_PROVIDER_BASE_URL")
	}
	var v struct {
		Username  string `json:"username"`
		Email     string `json:"email"`
		Password  string `json:"password"`
		SystemIDs string `json:"system_ids"`
		Utility   string `json:"utility"`
	}
	_ = json.Unmarshal([]byte(secret), &v)
	var legacy map[string]string
	_ = json.Unmarshal([]byte(secret), &legacy)
	provider := strings.ToLower(s.Provider)
	// The deployed Python service exposes split provider-specific variables.
	// Resolve them by provider so Enphase credentials can never be used for
	// Emporia or PG&E when all providers are configured together.
	switch provider {
	case "enphase":
		v.Username = firstNonEmpty(v.Username, firstNonEmpty(legacy["ENPHASE_USERNAME"], legacy["ENPHASE_EMAIL"]))
		v.Password = firstNonEmpty(v.Password, legacy["ENPHASE_PASSWORD"])
		v.SystemIDs = firstNonEmpty(v.SystemIDs, firstNonEmpty(legacy["ENPHASE_SYSTEM_IDS"], legacy["SYSTEM_IDS"]))
	case "emporia":
		v.Email = firstNonEmpty(v.Email, legacy["EMPORIA_EMAIL"])
		v.Password = firstNonEmpty(v.Password, legacy["EMPORIA_PASSWORD"])
	case "opower", "pge":
		v.Username = firstNonEmpty(v.Username, firstNonEmpty(legacy["PGE_USERNAME"], legacy["PGE_EMAIL"]))
		v.Password = firstNonEmpty(v.Password, legacy["PGE_PASSWORD"])
		v.Utility = firstNonEmpty(v.Utility, legacy["PGE_UTILITY"])
	}
	switch provider {
	case "enphase":
		if base == "" {
			base = "https://enlighten.enphaseenergy.com"
		}
		return &Enphase{Client: Client{BaseURL: base, HTTP: &http.Client{Jar: nil}}, Username: v.Username, Password: v.Password, SiteIDs: splitIDs(firstNonEmpty(s.SiteID, v.SystemIDs))}, nil
	case "emporia":
		if base == "" {
			base = "https://api.emporiaenergy.com"
		}
		return &Emporia{Client: Client{BaseURL: base, HTTP: http.DefaultClient}, Email: v.Email, Password: v.Password}, nil
	case "opower", "pge":
		if base == "" {
			base = "https://pge.opower.com"
		}
		return &Opower{Client: Client{BaseURL: base, HTTP: http.DefaultClient}, Username: v.Username, Password: v.Password, Utility: "pge"}, nil
	}
	return nil, fmt.Errorf("unsupported provider %q", s.Provider)
}
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func credentialFor(s domain.Setup) (string, error) {
	if v := os.Getenv(s.CredentialEnv); v != "" {
		return v, nil
	}
	keys := map[string][]string{"enphase": {"ENPHASE_USERNAME", "ENPHASE_EMAIL", "ENPHASE_PASSWORD", "ENPHASE_SYSTEM_IDS", "SYSTEM_IDS"}, "emporia": {"EMPORIA_EMAIL", "EMPORIA_PASSWORD"}, "opower": {"PGE_USERNAME", "PGE_EMAIL", "PGE_PASSWORD", "PGE_UTILITY"}, "pge": {"PGE_USERNAME", "PGE_EMAIL", "PGE_PASSWORD", "PGE_UTILITY"}}[strings.ToLower(s.Provider)]
	values := map[string]string{}
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			values[key] = v
		}
	}
	if len(values) == 0 {
		return "", perr(ErrMissingCredential, errors.New("credential is not configured"))
	}
	b, _ := json.Marshal(values)
	return string(b), nil
}
