// Package httpapi exposes the legacy REST contract during the staged Go migration.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/app"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/client"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
)

type Server struct{ App *app.App }
type publicReading struct {
	TS      string  `json:"ts"`
	Source  string  `json:"source"`
	Channel string  `json:"channel"`
	Watts   float64 `json:"watts"`
	KWh     float64 `json:"kwh"`
}
type publicReportReading struct {
	publicReading
	Raw any `json:"raw"`
}

func New(a *app.App) Server { return Server{App: a} }

// ValidateLoopbackAddress keeps the staged compatibility API private. Production
// exposure is an explicit cutover concern, not an incidental address override.
func ValidateLoopbackAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("API address must be host:port: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("API address has invalid port %q", port)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("staged API must bind a loopback address")
	}
	return nil
}

func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.App == nil {
		write(w, http.StatusServiceUnavailable, map[string]any{"status": "error", "error": "application unavailable"})
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		write(w, http.StatusOK, map[string]any{"ok": true, "service": "power-monitor"})
	case r.Method == http.MethodGet && r.URL.Path == "/api/status":
		write(w, http.StatusOK, s.status())
	case r.Method == http.MethodGet && r.URL.Path == "/api/enphase/systems":
		s.systems(w)
	case r.Method == http.MethodGet && r.URL.Path == "/api/devices":
		s.devices(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/usage":
		s.usage(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/report":
		s.report(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/collect":
		s.collect(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/pge/mfa/start":
		s.startMFA(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/pge/mfa/select":
		s.selectMFA(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/pge/mfa/verify":
		s.verifyMFA(w, r)
	default:
		write(w, http.StatusNotFound, map[string]any{"status": "error", "error": "not found"})
	}
}

func (s Server) status() map[string]any {
	providers := map[string]bool{"enphase": false, "emporia": false, "pge_opower": false}
	for _, setup := range s.App.Config.Setups {
		switch setup.Provider {
		case "enphase":
			providers["enphase"] = true
		case "emporia":
			providers["emporia"] = true
		case "pge", "opower":
			providers["pge_opower"] = true
		}
	}
	return map[string]any{"version": 1, "service": "power-monitor", "providers": providers}
}

func (s Server) systems(w http.ResponseWriter) {
	out := []map[string]any{}
	for _, setup := range s.App.Config.Setups {
		if setup.Provider == "enphase" {
			out = append(out, publicEnphase(setup))
		}
	}
	if len(out) == 0 {
		write(w, http.StatusServiceUnavailable, map[string]any{"status": "not_configured", "systems": out})
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "ok", "systems": out})
}
func (s Server) devices(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider != "" && provider != "enphase" && provider != "emporia" {
		write(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "unknown provider " + strconv.Quote(provider)})
		return
	}
	out := map[string][]map[string]any{}
	for _, setup := range s.App.Config.Setups {
		if setup.Provider != "enphase" && setup.Provider != "emporia" {
			continue
		}
		if provider == "" || provider == setup.Provider {
			if setup.Provider == "enphase" {
				out["enphase"] = append(out["enphase"], publicEnphase(setup))
			} else {
				out["emporia"] = append(out["emporia"], publicEmporia(setup))
			}
		}
	}
	write(w, http.StatusOK, map[string]any{"version": 1, "providers": out})
}
func publicEnphase(s domain.Setup) map[string]any {
	return map[string]any{"name": s.Name, "cloud": s.SiteID != "", "site_id": s.SiteID}
}
func publicEmporia(s domain.Setup) map[string]any {
	return map[string]any{"name": s.Name, "device_gid": s.DeviceGID}
}
func publicReadings(in []domain.Reading) []publicReading {
	out := make([]publicReading, 0, len(in))
	for _, r := range in {
		out = append(out, publicReading{TS: r.Timestamp.UTC().Format(time.RFC3339), Source: r.Provider, Channel: r.Channel, Watts: r.Watts, KWh: r.KWh})
	}
	return out
}
func publicReportReadings(in []domain.Reading) []publicReportReading {
	base := publicReadings(in)
	out := make([]publicReportReading, 0, len(base))
	for _, r := range base {
		out = append(out, publicReportReading{publicReading: r, Raw: nil})
	}
	return out
}
func (s Server) usage(w http.ResponseWriter, r *http.Request) {
	limit, ok := limit(r, 500, 5000)
	if !ok {
		write(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "limit must be an integer"})
		return
	}
	provider := r.URL.Query().Get("provider")
	rows := s.App.ReadingsFiltered(provider, "", timeZero(), timeZero())
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	write(w, http.StatusOK, map[string]any{"version": 1, "provider": nullIfEmpty(provider), "rows": publicReadings(rows)})
}
func (s Server) report(w http.ResponseWriter, r *http.Request) {
	rows := s.App.Readings()
	if len(rows) > 500 {
		rows = rows[len(rows)-500:]
	}
	write(w, http.StatusOK, map[string]any{"rows": publicReportReadings(rows)})
}
func (s Server) collect(w http.ResponseWriter, r *http.Request) {
	result, err := s.App.Collect(r.Context(), "")
	if err != nil {
		write(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	write(w, http.StatusOK, result)
}
func (s Server) pgeSetup(r *http.Request) (name string, configured bool, err error) {
	requested := strings.TrimSpace(r.URL.Query().Get("setup"))
	var candidates []string
	for _, setup := range s.App.Config.Setups {
		if setup.Provider != "pge" && setup.Provider != "opower" {
			continue
		}
		candidates = append(candidates, setup.Name)
		if requested != "" && setup.Name == requested {
			return setup.Name, true, nil
		}
	}
	if len(candidates) == 0 {
		return "", false, nil
	}
	if requested != "" {
		return "", true, fmt.Errorf("PG&E setup %q not found (candidates: %s)", requested, strings.Join(candidates, ", "))
	}
	if len(candidates) == 1 {
		return candidates[0], true, nil
	}
	return "", true, fmt.Errorf("multiple PG&E setups are configured; specify the setup query parameter")
}
func (s Server) startMFA(w http.ResponseWriter, r *http.Request) {
	name, configured, err := s.pgeSetup(r)
	if err != nil {
		write(w, http.StatusBadRequest, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	if !configured {
		write(w, http.StatusServiceUnavailable, map[string]any{"status": "not_configured"})
		return
	}
	options, err := s.App.StartMFA(r.Context(), name)
	if err != nil {
		write(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "mfa_required", "options": options})
}
func (s Server) selectMFA(w http.ResponseWriter, r *http.Request) {
	name, configured, err := s.pgeSetup(r)
	if err != nil {
		write(w, http.StatusBadRequest, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	if !configured {
		write(w, http.StatusConflict, map[string]any{"status": "error", "error": "Start MFA first"})
		return
	}
	var in struct {
		Option string `json:"option"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if in.Option != "Email" && in.Option != "Phone" {
		write(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "option must be Email or Phone"})
		return
	}
	if err := s.App.SelectMFA(r.Context(), name, in.Option); err != nil {
		write(w, http.StatusConflict, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "code_sent", "option": in.Option})
}
func (s Server) verifyMFA(w http.ResponseWriter, r *http.Request) {
	name, configured, err := s.pgeSetup(r)
	if err != nil {
		write(w, http.StatusBadRequest, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	if !configured {
		write(w, http.StatusConflict, map[string]any{"status": "error", "error": "Start MFA first"})
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if strings.TrimSpace(in.Code) == "" || len(in.Code) > 32 {
		write(w, http.StatusBadRequest, map[string]any{"status": "error", "error": "A valid MFA code is required"})
		return
	}
	if err := s.App.VerifyMFA(r.Context(), name, in.Code); err != nil {
		var providerErr *client.ProviderError
		if errors.As(err, &providerErr) && providerErr.Class == client.ErrMFARequired {
			write(w, http.StatusConflict, map[string]any{"status": "error", "error": err.Error()})
			return
		}
		write(w, http.StatusBadGateway, map[string]any{"status": "error", "error": err.Error()})
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "ok", "message": "PG&E MFA verified and login session saved"})
}
func limit(r *http.Request, fallback, maximum int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, false
	}
	if n > maximum {
		n = maximum
	}
	return n, true
}
func timeZero() time.Time { return time.Time{} }
func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func write(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
