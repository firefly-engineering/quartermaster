package dispatch

import (
	"context"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/send"
)

// agent starts harness h, named name, in a new tab of herdr workspace id, at ws, as a
// session's spawn does (send.Start), and prompts it. pane is the tab's pane once created, even
// when a later step fails.
func (d Dispatcher) agent(ctx context.Context, h config.Harness, id, ws, name, prompt string) (pane string, err error) {
	if pane, err = send.Start(ctx, d.herdr(), h, id, ws, name); err != nil {
		return pane, err
	}
	return pane, d.herdr().AgentPrompt(ctx, pane, h.Prompt(prompt))
}

// agentNames are the names of herdr's live agents. An agent not started or renamed through
// herdr has none.
func (d Dispatcher) agentNames(ctx context.Context) ([]string, error) {
	agents, err := d.herdr().AgentList(ctx)
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

// herdr is the client dispatch calls herdr through. Its errors are herdr.ExitError's, as send's
// and refresh's are.
func (d Dispatcher) herdr() herdr.Client { return herdr.New(d.Herdr, d.Env) }
