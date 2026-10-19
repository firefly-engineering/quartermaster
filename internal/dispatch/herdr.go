package dispatch

import (
	"context"

	"github.com/firefly-engineering/quartermaster/internal/herdr"
)

// herdr 0.9.5 has no call that creates a pane running a command, so an agent starts in three
// (spec §9.2), each checked against `herdr <cmd> --help`:
//
//	herdr tab create --workspace <id> --cwd <path> --label <text> --no-focus
//	herdr agent start <name> --kind <kind> --pane <id> [-- <agent-args>...]
//	herdr agent prompt <pane> <text>

// agent starts the configured worker, named name, in a new tab of herdr workspace id, at ws,
// and prompts it. pane is the tab's pane once created, even when a later step fails.
func (d Dispatcher) agent(ctx context.Context, id, ws, name, prompt string) (pane string, err error) {
	h := d.herdr()
	if pane, err = h.TabCreate(ctx, id, ws, name); err != nil {
		return "", err
	}
	w := d.Config.Harness.Worker
	if err := h.AgentStart(ctx, name, w.Kind, pane, w.Args); err != nil {
		return pane, err
	}
	if err := h.AgentPrompt(ctx, pane, prompt); err != nil {
		return pane, err
	}
	return pane, nil
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
