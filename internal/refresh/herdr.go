package refresh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// herdr's JSON, as herdr 0.9.5's API schema has it: every answer is `{"id", "result"}`, and an
// error is `{"id", "error": {"code", "message"}}` on standard error, with exit status 1.

type herdrError struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// errorCode is herdr's error code in err, "" when err is not one of herdr's.
func errorCode(err error) string {
	var exit *ExitError
	if !errors.As(err, &exit) {
		return ""
	}
	var e herdrError
	if json.Unmarshal(exit.Stderr, &e) != nil {
		return ""
	}
	return e.Error.Code
}

func (g *gatherer) herdr(ctx context.Context, v any, args ...string) error {
	if g.Herdr == nil {
		return errors.New("herdr: not available")
	}
	out, err := g.Herdr(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("herdr %s: %w", strings.Join(args[:min(2, len(args))], " "), err)
	}
	return nil
}

// worker asks herdr about the pane a dispatched item's agent was started in. A pane that is
// gone, or that no agent occupies any more, is a worker that exited: it returns the WAITING
// summary. Otherwise it returns why the item stays as it is.
func (g *gatherer) worker(ctx context.Context, pane string) (waiting, reason string) {
	var r struct {
		Result struct {
			Pane struct {
				Agent  *string `json:"agent"`
				Status string  `json:"agent_status"`
			} `json:"pane"`
		} `json:"result"`
	}
	err := g.herdr(ctx, &r, "pane", "get", pane)
	switch {
	case errorCode(err) == "pane_not_found":
		return "worker exited: pane " + pane + " is gone", ""
	case err != nil:
		return "", "herdr: " + err.Error()
	case r.Result.Pane.Agent == nil || *r.Result.Pane.Agent == "":
		return "worker exited: no agent in pane " + pane, ""
	}
	return "", fmt.Sprintf("its worker %s in pane %s is %s", *r.Result.Pane.Agent, pane, r.Result.Pane.Status)
}

type agent struct {
	Workspace string `json:"workspace_id"`
	Pane      string `json:"pane_id"`
	Status    string `json:"agent_status"`
}

// agents lists herdr's agents, once per refresh.
func (g *gatherer) agents(ctx context.Context) ([]agent, error) {
	if !g.agentsRead {
		var r struct {
			Result struct {
				Agents []agent `json:"agents"`
			} `json:"result"`
		}
		g.agentsErr = g.herdr(ctx, &r, "agent", "list")
		g.agentList, g.agentsRead = r.Result.Agents, true
	}
	return g.agentList, g.agentsErr
}

// workspaces lists the ids of herdr's workspaces by label, once per refresh.
func (g *gatherer) workspaces(ctx context.Context) (map[string][]string, error) {
	if g.spaces == nil && g.spacesErr == nil {
		var r struct {
			Result struct {
				Workspaces []struct {
					ID    string `json:"workspace_id"`
					Label string `json:"label"`
				} `json:"workspaces"`
			} `json:"result"`
		}
		if g.spacesErr = g.herdr(ctx, &r, "workspace", "list"); g.spacesErr == nil {
			g.spaces = map[string][]string{}
			for _, w := range r.Result.Workspaces {
				g.spaces[w.Label] = append(g.spaces[w.Label], w.ID)
			}
		}
	}
	return g.spaces, g.spacesErr
}

// replier finds the agent an item was sent to: the one in its dispatch marker's pane, else the
// one agent in the workspace labelled label, as `worklog send` finds it.
func (g *gatherer) replier(ctx context.Context, pane, label string) (agent, error) {
	agents, err := g.agents(ctx)
	if err != nil {
		return agent{}, err
	}
	if pane != "" {
		i := slices.IndexFunc(agents, func(a agent) bool { return a.Pane == pane })
		if i < 0 {
			return agent{}, fmt.Errorf("no agent in pane %s", pane)
		}
		return agents[i], nil
	}
	spaces, err := g.workspaces(ctx)
	if err != nil {
		return agent{}, err
	}
	if len(spaces[label]) != 1 {
		return agent{}, fmt.Errorf("%d herdr workspaces labelled %q", len(spaces[label]), label)
	}
	var in []agent
	for _, a := range agents {
		if a.Workspace == spaces[label][0] {
			in = append(in, a)
		}
	}
	if len(in) != 1 {
		return agent{}, fmt.Errorf("%d agents in workspace %q; expected one", len(in), label)
	}
	return in[0], nil
}

// readLines is how much of an agent's recent output refresh reads for a reply.
const readLines = "400"

// reply reads the agent item id was sent to, by its pane or its workspace label, for its reply
// to the item's prompt. It returns the reply's GitHub links not in the item's line already, or
// why there are none to record.
func (g *gatherer) reply(ctx context.Context, id, pane, label, title string) (links []string, reason string) {
	if pane == "" && label == "" {
		return nil, "no pane or workspace label to read a reply from"
	}
	who := label
	if pane != "" {
		who = "pane " + pane
	}
	a, err := g.replier(ctx, pane, label)
	if err != nil {
		return nil, "herdr: " + err.Error()
	}
	if a.Status != "idle" && a.Status != "done" {
		return nil, fmt.Sprintf("the agent in %s is %s", who, a.Status)
	}
	var r struct {
		Result struct {
			Read struct {
				Text string `json:"text"`
			} `json:"read"`
		} `json:"result"`
	}
	if err := g.herdr(ctx, &r, "agent", "read", a.Pane, "--source", "recent-unwrapped", "--lines", readLines); err != nil {
		return nil, "herdr: " + err.Error()
	}
	all, ok := replyLinks(r.Result.Read.Text, id)
	if !ok {
		return nil, fmt.Sprintf("no prompt for ^%s in the recent output of %s", id, who)
	}
	had := githubLinks(title)
	for _, l := range all {
		if !slices.Contains(had, l) {
			links = append(links, l)
		}
	}
	if len(links) == 0 {
		return nil, "no reply links from " + who + " yet"
	}
	return links, ""
}
