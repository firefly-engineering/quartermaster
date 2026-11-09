package dispatch

import (
	"context"
	"fmt"
	"slices"

	"github.com/firefly-engineering/quartermaster/internal/rex"
)

// Kept is why CloseTab leaves a fresh dispatch's tab open: its agent was renamed, or is not idle
// or done.
type Kept struct {
	Pane   string
	Name   string // the agent's name
	Status string // its status; "" when it keeps its tab for its name
}

func (k *Kept) Error() string {
	if k.Status == "" {
		return fmt.Sprintf("its worker in pane %s is named %q, not as its dispatch named it, so it keeps its tab", k.Pane, k.Name)
	}
	return fmt.Sprintf("its worker %s in pane %s is %s: its tab closes once it is idle or done", k.Name, k.Pane, k.Status)
}

// Running reports whether the kept agent is working or blocked: running a turn, or waiting on
// a prompt.
func (k *Kept) Running() bool { return k.Status == rex.Working || k.Status == rex.Blocked }

// CloseTab closes the tab of pane, where a fresh dispatch in workspace ws started its
// agent, once the agent is idle or done and still bears the name the dispatch gave it, or once
// no agent runs in the pane. agents lists rex's agents, asked only when the pane holds one.
// It returns the tab it closed, "" when the pane or its tab was gone already. A *Kept error is
// why the tab stays: renaming the agent is how the developer keeps it for good. Any other error
// is rex's.
//
// refresh closes a finished item's tab with it, and a fresh re-dispatch its previous worker's
// (spec §9.2): both under these guards.
func CloseTab(ctx context.Context, c rex.Client, agents func(context.Context) ([]rex.Agent, error), pane, ws string) (tab string, err error) {
	p, err := c.PaneGet(ctx, pane)
	switch {
	case rex.Code(err) == rex.PaneNotFound:
		return "", nil
	case err != nil:
		return "", err
	}
	if p.Kind != "" {
		all, err := agents(ctx)
		if err != nil {
			return "", err
		}
		var name string
		if i := slices.IndexFunc(all, func(a rex.Agent) bool { return a.Pane == pane }); i >= 0 {
			name = all[i].Name
		}
		if !IsFreshAgent(name, ws) {
			return "", &Kept{Pane: pane, Name: name}
		}
		if p.Status != rex.Idle && p.Status != rex.Done {
			return "", &Kept{Pane: pane, Name: name, Status: p.Status}
		}
	}
	err = c.TabClose(ctx, p.Tab)
	switch {
	case rex.Code(err) == rex.TabNotFound:
		return "", nil
	case err != nil:
		return "", fmt.Errorf("closing tab %s of pane %s: %w", p.Tab, pane, err)
	}
	return p.Tab, nil
}
