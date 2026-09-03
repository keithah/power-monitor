package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/app"
	"net/http"
	"time"
)

type Server struct {
	App   *app.App
	Token string
}
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  map[string]any  `json:"params"`
}

func (s Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.Token != "" && r.Header.Get("Authorization") != "Bearer "+s.Token {
		http.Error(w, "unauthorized", 401)
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	var q request
	if err := dec.Decode(&q); err != nil {
		writeErr(w, nil, -32700, "parse error")
		return
	}
	if q.JSONRPC != "2.0" || q.Method == "" {
		writeErr(w, q.ID, -32600, "invalid request")
		return
	}
	if q.Method == "notifications/initialized" || len(q.ID) == 0 || string(q.ID) == "null" {
		if q.Method == "notifications/initialized" {
			w.WriteHeader(202)
		} else {
			writeErr(w, q.ID, -32600, "request id required")
		}
		return
	}
	if q.Method == "tools/call" {
		if _, ok := q.Params["name"].(string); !ok {
			writeErr(w, q.ID, -32602, "tools/call requires params.name")
			return
		}
	}
	switch q.Method {
	case "initialize":
		writeResult(w, q.ID, map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "power-monitor", "version": "0.0.0-dev"}})
	case "tools/list":
		writeResult(w, q.ID, map[string]any{"tools": tools()})
	case "tools/call":
		res, err := call(s, q.Params)
		if err != nil {
			writeErr(w, q.ID, -32602, err.Error())
		} else {
			writeResult(w, q.ID, res)
		}
	default:
		writeErr(w, q.ID, -32601, "method not found")
	}
}
func writeResult(w http.ResponseWriter, id json.RawMessage, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": v})
}
func writeErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "error": map[string]any{"code": code, "message": msg}})
}
func tools() []map[string]any {
	names := []string{"status", "setup_list", "setup_show", "device_list", "collect_status", "usage", "summary", "aggregate", "report", "doctor", "pge_mfa_start", "pge_mfa_select", "pge_mfa_verify"}
	o := make([]map[string]any, len(names))
	for i, n := range names {
		o[i] = map[string]any{"name": n, "description": "read-only power monitor operation", "inputSchema": map[string]any{"type": "object"}}
	}
	return o
}
func call(s Server, p map[string]any) (any, error) {
	if s.App == nil {
		return nil, errors.New("application unavailable")
	}
	n, _ := p["name"].(string)
	args, _ := p["arguments"].(map[string]any)
	if args == nil {
		args = p
	}
	switch n {
	case "status":
		return s.App.Status(), nil
	case "setup_list", "device_list":
		return s.App.Config.Setups, nil
	case "setup_show":
		name, _ := args["name"].(string)
		for _, v := range s.App.Config.Setups {
			if v.Name == name {
				return v, nil
			}
		}
		return nil, errors.New("setup not found")
	case "collect_status":
		return s.App.Collect(context.Background(), "")
	case "aggregate":
		name, _ := args["rollup"].(string)
		v, e := s.App.Aggregate(name)
		if e != nil {
			return nil, e
		}
		return map[string]any{"rollup": name, "kwh": v}, nil
	case "usage":
		return s.App.ReadingsFiltered(stringArg(args, "provider"), stringArg(args, "setup"), parseRFC3339(args, "from"), parseRFC3339(args, "to")), nil
	case "summary":
		return s.App.Summary(stringArg(args, "period"), parseRFC3339(args, "from"), parseRFC3339(args, "to"))
	case "report":
		rs := s.App.ReadingsFiltered(stringArg(args, "provider"), stringArg(args, "setup"), parseRFC3339(args, "from"), parseRFC3339(args, "to"))
		return map[string]any{"readings": len(rs), "data": rs}, nil
	case "doctor":
		return map[string]any{"ok": app.Validate(s.App.Config) == nil}, nil
	case "pge_mfa_start":
		v, err := s.App.StartMFA(context.Background(), stringArg(args, "setup"))
		return map[string]any{"options": v}, err
	case "pge_mfa_select":
		return map[string]any{"status": "selected"}, s.App.SelectMFA(context.Background(), stringArg(args, "setup"), stringArg(args, "option"))
	case "pge_mfa_verify":
		return map[string]any{"status": "verified"}, s.App.VerifyMFA(context.Background(), stringArg(args, "setup"), stringArg(args, "code"))
	default:
		return nil, errors.New("unknown tool")
	}
}

func stringArg(args map[string]any, key string) string { v, _ := args[key].(string); return v }
func parseRFC3339(args map[string]any, key string) time.Time {
	v := stringArg(args, key)
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, v)
	return t
}
