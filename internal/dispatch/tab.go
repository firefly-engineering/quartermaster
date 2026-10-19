package dispatch

import (
	"context"
	"fmt"
	"slices"

	"github.com/firefly-engineering/quartermaster/internal/herdr"
)

// Kept is why CloseTab leaves a fresh dispatch's tab open: its agent was renamed, or is not idle
// or done.
type Kept struct {
	Pane   string
	Name   string // the agent's herdr name
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
func (k *Kept) Running() bool { return k.Status == herdr.Working || k.Status == herdr.Blocked }

// CloseTab closes the herdr tab of pane, where a fresh dispatch in workspace ws started its
// agent, once the agent is idle or done and still bears the name the dispatch gave it, or once
// no agent runs in the pane. agents lists herdr's agents, asked only when the pane holds one.
// It returns the tab it closed, "" when the pane or its tab was gone already. A *Kept error is
// why the tab stays: renaming the agent is how the developer keeps it for good. Any other error
// is herdr's.
//
// refresh closes a finished item's tab with it, and a fresh re-dispatch its previous worker's
// (spec §9.2): both under these guards.
func CloseTab(ctx context.Context, h herdr.Client, agents func(context.Context) ([]herdr.Agent, error), pane, ws string) (tab string, err error) {
	p, err := h.PaneGet(ctx, pane)
	switch {
	case herdr.Code(err) == herdr.PaneNotFound:
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
		if i := slices.IndexFunc(all, func(a herdr.Agent) bool { return a.Pane == pane }); i >= 0 {
			name = all[i].Name
		}
		if !IsFreshAgent(name, ws) {
			return "", &Kept{Pane: pane, Name: name}
		}
		if p.Status != herdr.Idle && p.Status != herdr.Done {
			return "", &Kept{Pane: pane, Name: name, Status: p.Status}
		}
	}
	err = h.TabClose(ctx, p.Tab)
	switch {
	case herdr.Code(err) == herdr.TabNotFound:
		return "", nil
	case err != nil:
		return "", fmt.Errorf("closing tab %s of pane %s: %w", p.Tab, pane, err)
	}
	return p.Tab, nil
}
