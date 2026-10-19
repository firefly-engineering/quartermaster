package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
)

// herdr 0.9.5 has no call that creates a pane running a command, so an agent starts in three
// (spec §9.2), each checked against `herdr <cmd> --help`:
//
//	herdr tab create --workspace <id> --cwd <path> --label <text> --no-focus
//	herdr agent start <name> --kind <kind> --pane <id> [-- <agent-args>...]
//	herdr agent prompt <pane> <text>
//
// `tab create` answers JSON, the new tab's pane in .result.root_pane.pane_id.

// agent starts the configured worker, named name, in a new tab of herdr workspace id, at ws,
// and prompts it. pane is the tab's pane once created, even when a later step fails.
func (d Dispatcher) agent(ctx context.Context, id, ws, name, prompt string) (pane string, err error) {
	var tab struct {
		Result struct {
			RootPane struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := d.herdrJSON(ctx, &tab, "tab", "create", "--workspace", id, "--cwd", ws, "--label", name, "--no-focus"); err != nil {
		return "", err
	}
	if pane = tab.Result.RootPane.ID; pane == "" {
		return "", fmt.Errorf("herdr tab create: no root pane in its answer")
	}
	w := d.Config.Harness.Worker
	start := []string{"agent", "start", name, "--kind", w.Kind, "--pane", pane}
	if len(w.Args) > 0 {
		start = append(append(start, "--"), w.Args...)
	}
	if _, err := d.herdr(ctx, start...); err != nil {
		return pane, err
	}
	if _, err := d.herdr(ctx, "agent", "prompt", pane, prompt); err != nil {
		return pane, err
	}
	return pane, nil
}

// agentNames are the names of herdr's live agents. `herdr agent list` answers
// .result.agents[].name, absent for an agent not started or renamed through herdr.
func (d Dispatcher) agentNames(ctx context.Context) ([]string, error) {
	var agents struct {
		Result struct {
			Agents []struct {
				Name string `json:"name"`
			} `json:"agents"`
		} `json:"result"`
	}
	if err := d.herdrJSON(ctx, &agents, "agent", "list"); err != nil {
		return nil, err
	}
	var names []string
	for _, a := range agents.Result.Agents {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return names, nil
}

func (d Dispatcher) herdrJSON(ctx context.Context, v any, args ...string) error {
	out, err := d.herdr(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		return fmt.Errorf("herdr %s %s: %w", args[0], args[1], err)
	}
	return nil
}

func (d Dispatcher) herdr(ctx context.Context, args ...string) (string, error) {
	return d.run(ctx, "herdr", d.Herdr, "", args...)
}
