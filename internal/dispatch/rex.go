package dispatch

import (
	"context"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/rex"
	"github.com/firefly-engineering/quartermaster/internal/send"
)

// agent starts harness h, named name, in a new tab of workspace id, at ws, as a session's spawn
// does (send.Start), and prompts it. pane is the tab's pane once created, even when a later
// step fails.
func (d Dispatcher) agent(ctx context.Context, h config.Harness, id, ws, name, prompt string) (pane string, err error) {
	c := d.client()
	if pane, err = send.Start(ctx, c, h, id, ws, name); err != nil {
		return pane, err
	}
	return pane, c.AgentPrompt(ctx, pane, h.Prompt(prompt))
}

// agentNames are the names of rex's live agents. An agent whose block has no label has none.
func (d Dispatcher) agentNames(ctx context.Context) ([]string, error) {
	agents, err := d.client().AgentList(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, a := range agents {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return names, nil
}

// client is the client dispatch calls rex through. Its errors are rex.ExitError's, as send's
// and refresh's are.
func (d Dispatcher) client() rex.Client { return rex.New(d.Rex, d.Env) }
