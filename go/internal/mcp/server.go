package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
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
	names := []string{"status", "setup_list", "setup_show", "device_list", "usage", "summary", "aggregate", "report", "doctor"}
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
	case "collect_status", "pge_mfa_start", "pge_mfa_select", "pge_mfa_verify":
		return nil, errors.New("mutating operation is not available through the read-only MCP server")
	case "aggregate":
		name, _ := args["rollup"].(string)
		v, e := s.App.Aggregate(name)
		if e != nil {
			return nil, e
		}
		return map[string]any{"rollup": name, "kwh": v}, nil
	case "usage":
		from, to, err := parseRange(args)
		if err != nil {
			return nil, err
		}
		return s.App.ReadingsFiltered(stringArg(args, "provider"), stringArg(args, "setup"), from, to), nil
	case "summary":
		from, to, err := parseRange(args)
		if err != nil {
			return nil, err
		}
		return s.App.Summary(stringArg(args, "period"), from, to)
	case "report":
		from, to, err := parseRange(args)
		if err != nil {
			return nil, err
		}
		rs := s.App.ReadingsFiltered(stringArg(args, "provider"), stringArg(args, "setup"), from, to)
		return map[string]any{"readings": len(rs), "data": rs}, nil
	case "doctor":
		return map[string]any{"ok": app.Validate(s.App.Config) == nil}, nil
	default:
		return nil, errors.New("unknown tool")
	}
}

func stringArg(args map[string]any, key string) string { v, _ := args[key].(string); return v }
func parseRange(args map[string]any) (time.Time, time.Time, error) {
	from, err := parseRFC3339(args, "from")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	to, err := parseRFC3339(args, "to")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return from, to, nil
}
func parseRFC3339(args map[string]any, key string) (time.Time, error) {
	v := stringArg(args, key)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", key, err)
	}
	return t, nil
}
