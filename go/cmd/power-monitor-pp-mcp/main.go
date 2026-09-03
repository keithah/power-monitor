package main

import (
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/app"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/config"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/mcp"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/store"
	"net/http"
	"os"
)

func main() {
	c, _ := config.Load(config.DefaultPath())
	db := config.DBPath()
	st, e := store.Open(db)
	if e != nil {
		panic(e)
	}
	defer st.Close()
	addr := os.Getenv("POWER_MONITOR_MCP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8095"
	}
	http.ListenAndServe(addr, mcp.Server{App: app.New(c, st), Token: os.Getenv("POWER_MONITOR_MCP_TOKEN")})
}
