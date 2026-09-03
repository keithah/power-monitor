package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/app"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/config"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/store"
	"github.com/spf13/cobra"
	"os"
	"time"
)

var cfgPath string

func load() (*app.App, error) {
	c, e := config.Load(cfgPath)
	if e != nil {
		return nil, e
	}
	db := config.DBPath()
	st, e := store.Open(db)
	if e != nil {
		return nil, e
	}
	return app.New(c, st), nil
}
func out(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }
func Execute() error {
	root := &cobra.Command{Use: "power-monitor-pp-cli", Version: "0.0.0-dev", SilenceUsage: true}
	root.PersistentFlags().StringVar(&cfgPath, "config", config.DefaultPath(), "configuration file")
	root.PersistentFlags().Bool("json", false, "emit JSON")
	root.PersistentFlags().Bool("compact", false, "compact JSON")
	root.PersistentFlags().Bool("agent", false, "agent-friendly output")
	root.PersistentFlags().String("select", "", "selection expression")
	root.PersistentFlags().Bool("dry-run", false, "do not persist changes")
	root.AddCommand(cmdStatus(), cmdAgentContext(), cmdDoctor(), cmdCollect(), cmdUsage(), cmdSummary(), cmdAggregate(), cmdReport(), cmdDevice(), cmdSetup(), cmdConfig(), cmdPGE())
	return root.Execute()
}
func run(fn func(*app.App) any) error {
	a, e := load()
	if e != nil {
		return e
	}
	defer a.Store.Close()
	return out(fn(a))
}
func cmdAgentContext() *cobra.Command {
	return &cobra.Command{Use: "agent-context", Short: "Describe safe agent-facing capabilities", RunE: func(*cobra.Command, []string) error {
		return out(map[string]any{"commands": []map[string]any{{"name": "status", "runnable": true}}})
	}}
}

func cmdStatus() *cobra.Command {
	return &cobra.Command{Use: "status", Example: "  power-monitor-pp-cli status", RunE: func(*cobra.Command, []string) error { return run(func(a *app.App) any { return a.Status() }) }}
}
func cmdDoctor() *cobra.Command {
	return &cobra.Command{Use: "doctor", RunE: func(*cobra.Command, []string) error {
		return run(func(a *app.App) any {
			bad := []string{}
			if e := config.Validate(a.Config); e != nil {
				bad = append(bad, e.Error())
			}
			return map[string]any{"ok": len(bad) == 0, "errors": bad}
		})
	}}
}
func cmdCollect() *cobra.Command {
	var setup string
	c := &cobra.Command{Use: "collect", RunE: func(*cobra.Command, []string) error {
		a, e := load()
		if e != nil {
			return e
		}
		defer a.Store.Close()
		v, e := a.Collect(context.Background(), setup)
		if e != nil {
			return e
		}
		return out(v)
	}}
	c.Flags().StringVar(&setup, "setup", "", "named setup")
	return c
}
func cmdUsage() *cobra.Command {
	var provider, setup, from, to string
	c := &cobra.Command{Use: "usage", RunE: func(*cobra.Command, []string) error {
		return run(func(a *app.App) any {
			start, end := parseTime(from), parseTime(to)
			return a.ReadingsFiltered(provider, setup, start, end)
		})
	}}
	c.Flags().StringVar(&provider, "provider", "", "provider filter")
	c.Flags().StringVar(&setup, "setup", "", "setup filter")
	c.Flags().StringVar(&from, "from", "", "inclusive RFC3339 start time")
	c.Flags().StringVar(&to, "to", "", "inclusive RFC3339 end time")
	return c
}
func cmdSummary() *cobra.Command {
	var period, from, to string
	c := &cobra.Command{Use: "summary", RunE: func(*cobra.Command, []string) error {
		a, e := load()
		if e != nil {
			return e
		}
		defer a.Store.Close()
		v, e := a.Summary(period, parseTime(from), parseTime(to))
		if e != nil {
			return e
		}
		return out(v)
	}}
	c.Flags().StringVar(&period, "period", "day", "day, week, month, or year")
	c.Flags().StringVar(&from, "from", "", "inclusive RFC3339 start time")
	c.Flags().StringVar(&to, "to", "", "inclusive RFC3339 end time")
	return c
}

func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, v)
	return t
}
func cmdAggregate() *cobra.Command {
	return &cobra.Command{Use: "aggregate <rollup>", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		a, e := load()
		if e != nil {
			return e
		}
		defer a.Store.Close()
		v, e := a.Aggregate(args[0])
		if e != nil {
			return e
		}
		return out(map[string]any{"rollup": args[0], "kwh": v})
	}}
}
func cmdReport() *cobra.Command {
	var provider, setup, from, to string
	c := &cobra.Command{Use: "report", RunE: func(*cobra.Command, []string) error {
		return run(func(a *app.App) any {
			rs := a.ReadingsFiltered(provider, setup, parseTime(from), parseTime(to))
			return map[string]any{"status": a.Status(), "readings": len(rs), "data": rs}
		})
	}}
	c.Flags().StringVar(&provider, "provider", "", "provider filter")
	c.Flags().StringVar(&setup, "setup", "", "setup filter")
	c.Flags().StringVar(&from, "from", "", "inclusive RFC3339 start time")
	c.Flags().StringVar(&to, "to", "", "inclusive RFC3339 end time")
	return c
}
func cmdDevice() *cobra.Command {
	p := &cobra.Command{Use: "device"}
	var provider string
	list := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error {
		return run(func(a *app.App) any {
			out := []domain.Setup{}
			for _, s := range a.Config.Setups {
				if provider == "" || s.Provider == provider {
					out = append(out, s)
				}
			}
			return out
		})
	}}
	list.Flags().StringVar(&provider, "provider", "", "provider filter")
	p.AddCommand(list)
	return p
}
func cmdSetup() *cobra.Command {
	p := &cobra.Command{Use: "setup"}
	p.AddCommand(&cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return run(func(a *app.App) any { return a.Config.Setups }) }}, &cobra.Command{Use: "show <name>", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		a, e := load()
		if e != nil {
			return e
		}
		defer a.Store.Close()
		s, e := config.Select(a.Config, args[0])
		if e != nil {
			return e
		}
		return out(s)
	}}, &cobra.Command{Use: "add", RunE: func(cmd *cobra.Command, args []string) error {
		var s domain.Setup
		s.Name, _ = cmd.Flags().GetString("name")
		s.Provider, _ = cmd.Flags().GetString("provider")
		s.CredentialEnv, _ = cmd.Flags().GetString("credential-env")
		s.Role = domain.Role(func() string { x, _ := cmd.Flags().GetString("role"); return x }())
		c, e := config.Load(cfgPath)
		if e != nil {
			return e
		}
		c.Setups = append(c.Setups, s)
		return config.Save(cfgPath, c)
	}})
	add := p.Commands()[2]
	add.Flags().String("name", "", "setup name")
	add.Flags().String("provider", "", "provider")
	add.Flags().String("credential-env", "", "environment reference")
	add.Flags().String("role", "", "role")
	return p
}
func cmdPGE() *cobra.Command {
	p := &cobra.Command{Use: "pge"}
	var setup, option, code string
	start := &cobra.Command{Use: "mfa-start", RunE: func(cmd *cobra.Command, _ []string) error {
		a, e := load()
		if e != nil {
			return e
		}
		defer a.Store.Close()
		v, e := a.StartMFA(context.Background(), setup)
		if e != nil {
			return e
		}
		return out(map[string]any{"options": v})
	}}
	selectCmd := &cobra.Command{Use: "mfa-select", RunE: func(cmd *cobra.Command, _ []string) error {
		a, e := load()
		if e != nil {
			return e
		}
		defer a.Store.Close()
		return a.SelectMFA(context.Background(), setup, option)
	}}
	verify := &cobra.Command{Use: "mfa-verify", RunE: func(cmd *cobra.Command, _ []string) error {
		agent, _ := cmd.Flags().GetBool("agent")
		if code == "" && !agent {
			fmt.Fprint(os.Stderr, "MFA code: ")
			code, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
		if code == "" {
			return fmt.Errorf("--code is required in agent mode")
		}
		a, e := load()
		if e != nil {
			return e
		}
		defer a.Store.Close()
		if e = a.VerifyMFA(context.Background(), setup, code); e != nil {
			return e
		}
		return out(map[string]string{"status": "verified"})
	}}
	for _, c := range []*cobra.Command{start, selectCmd, verify} {
		c.Flags().StringVar(&setup, "setup", "", "named PG&E setup")
	}
	selectCmd.Flags().StringVar(&option, "option", "", "Email or Phone")
	verify.Flags().StringVar(&code, "code", "", "one-time MFA code")
	p.AddCommand(start, selectCmd, verify)
	return p
}
func cmdConfig() *cobra.Command {
	p := &cobra.Command{Use: "config"}
	p.AddCommand(&cobra.Command{Use: "show", RunE: func(*cobra.Command, []string) error {
		c, e := config.Load(cfgPath)
		if e != nil {
			return e
		}
		return out(c)
	}}, &cobra.Command{Use: "validate", RunE: func(*cobra.Command, []string) error {
		c, e := config.Load(cfgPath)
		if e != nil {
			return e
		}
		if e = config.Validate(c); e != nil {
			return e
		}
		fmt.Println("valid")
		return nil
	}})
	return p
}
func ExitCode(error) int { return 1 }
