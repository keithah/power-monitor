package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/app"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/config"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/mcp"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/store"
)

func run() error {
	c, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	st, err := store.Open(config.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	addr := os.Getenv("POWER_MONITOR_MCP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8095"
	}
	return http.ListenAndServe(addr, mcp.Server{App: app.New(c, st), Token: os.Getenv("POWER_MONITOR_MCP_TOKEN")})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
