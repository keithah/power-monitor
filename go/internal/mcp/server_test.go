package mcp

import (
	"bytes"
	"encoding/json"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/app"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"net/http/httptest"
	"testing"
)

func TestInitializeToolsAndAuth(t *testing.T) {
	s := Server{App: app.New(domain.Config{}, nil), Token: "secret"}
	for _, method := range []string{"initialize", "tools/list"} {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method})
		r := httptest.NewRequest("POST", "/", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %d", method, w.Code)
		}
		if method == "tools/list" && !bytes.Contains(w.Body.Bytes(), []byte(`"summary"`)) {
			t.Fatalf("summary tool missing: %s", w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("auth")
	}
}

func TestToolsCallRequiresName(t *testing.T) {
	s := Server{App: app.New(domain.Config{}, nil)}
	r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`)))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("-32602")) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}
