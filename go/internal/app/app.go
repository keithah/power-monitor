package app

import (
	"context"
	"fmt"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/client"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/config"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/domain"
	"github.com/mvanhorn/printing-press-library/library/power-monitor/internal/store"
	"time"
)

type App struct {
	Config    domain.Config
	Store     *store.Store
	Now       func() time.Time
	Providers map[string]client.Provider
}

func New(c domain.Config, st *store.Store) *App {
	return &App{Config: c, Store: st, Providers: map[string]client.Provider{}}
}
func (a *App) Status() map[string]any {
	return map[string]any{"setups": len(a.Config.Setups), "rollups": len(a.Config.Rollups), "storage": a.Store != nil}
}
func (a *App) Collect(ctx context.Context, name string) (map[string]any, error) {
	result := map[string]any{}
	for _, s := range a.Config.Setups {
		if name != "" && s.Name != name {
			continue
		}
		p := a.Providers[s.Name]
		if p == nil {
			var err error
			p, err = client.Configured(s)
			if err != nil {
				result[s.Name] = map[string]any{"status": "error", "error": err.Error()}
				continue
			}
		}
		rs, e := p.Collect(ctx, s)
		if e != nil {
			result[s.Name] = map[string]any{"status": "error", "error": e.Error()}
			continue
		}
		n := 0
		if a.Store != nil {
			n, e = a.Store.Put(rs)
			if e != nil {
				return result, e
			}
		}
		result[s.Name] = map[string]any{"status": "ok", "readings": len(rs), "inserted": n}
	}
	return result, nil
}
func (a *App) Readings() []domain.Reading {
	return a.ReadingsFiltered("", "", time.Time{}, time.Time{})
}
func (a *App) ReadingsFiltered(provider, setup string, from, to time.Time) []domain.Reading {
	if a.Store == nil {
		return nil
	}
	r, _ := a.Store.ListFiltered(provider, setup, from, to)
	return r
}
func (a *App) Aggregate(name string) (float64, error) {
	for _, r := range a.Config.Rollups {
		if r.Name == name {
			return domain.Aggregate(a.Config, r, a.Readings())
		}
	}
	return 0, fmt.Errorf("rollup %q not found", name)
}
func (a *App) pge(ctx context.Context, name string) (*client.Opower, error) {
	p := a.Providers[name]
	if p == nil {
		s, err := config.Select(a.Config, name)
		if err != nil {
			return nil, err
		}
		p, err = client.Configured(s)
		if err != nil {
			return nil, err
		}
		a.Providers[name] = p
	}
	o, ok := p.(*client.Opower)
	if !ok {
		return nil, fmt.Errorf("setup %q is not a PG&E provider", name)
	}
	return o, nil
}
func (a *App) StartMFA(ctx context.Context, name string) ([]client.MFAOption, error) {
	o, err := a.pge(ctx, name)
	if err != nil {
		return nil, err
	}
	return o.StartMFA(ctx)
}
func (a *App) SelectMFA(ctx context.Context, name, option string) error {
	o, err := a.pge(ctx, name)
	if err != nil {
		return err
	}
	return o.SelectMFA(ctx, option)
}
func (a *App) VerifyMFA(ctx context.Context, name, code string) error {
	o, err := a.pge(ctx, name)
	if err != nil {
		return err
	}
	return o.VerifyMFA(ctx, code)
}
func Validate(c domain.Config) error { return config.Validate(c) }
