package refresh

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/firefly-engineering/quartermaster/internal/dispatch"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
)

// worker asks herdr about the pane a dispatched item's agent was started in. A pane that is
// gone, or that no agent occupies any more, is a worker that exited: it returns the WAITING
// summary. Otherwise it returns why the item stays as it is.
func (g *gatherer) worker(ctx context.Context, pane string) (waiting, reason string) {
	p, err := g.Herdr.PaneGet(ctx, pane)
	switch {
	case herdr.Code(err) == herdr.PaneNotFound:
		return "worker exited: pane " + pane + " is gone", ""
	case err != nil:
		return "", "herdr: " + err.Error()
	case p.Kind == "":
		return "worker exited: no agent in pane " + pane, ""
	}
	return "", fmt.Sprintf("its worker %s in pane %s is %s", p.Kind, pane, p.Status)
}

// closeTab closes the herdr tab of pane, where a fresh dispatch in workspace ws started its
// agent, under dispatch.CloseTab's guards. It returns the tab it closed, "" when the pane or its
// tab was gone already; or why the pane stays, for this refresh, or for good when its agent was
// renamed.
func (g *gatherer) closeTab(ctx context.Context, pane, ws string) (tab, reason string) {
	tab, err := dispatch.CloseTab(ctx, g.Herdr, g.agents, pane, ws)
	var kept *dispatch.Kept
	switch {
	case errors.As(err, &kept):
		return "", err.Error()
	case err != nil:
		return "", err.Error() + "; retried next refresh"
	}
	return tab, ""
}

type agent = herdr.Agent

// agents lists herdr's agents, once per refresh.
func (g *gatherer) agents(ctx context.Context) ([]agent, error) {
	if !g.agentsRead {
		g.agentList, g.agentsErr = g.Herdr.AgentList(ctx)
		g.agentsRead = true
	}
	return g.agentList, g.agentsErr
}

// workspaces lists the ids of herdr's workspaces by label, once per refresh.
func (g *gatherer) workspaces(ctx context.Context) (map[string][]string, error) {
	if g.spaces == nil && g.spacesErr == nil {
		var ws []herdr.Workspace
		if ws, g.spacesErr = g.Herdr.WorkspaceList(ctx); g.spacesErr == nil {
			g.spaces = map[string][]string{}
			for _, w := range ws {
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
const readLines = 400

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
	text, err := g.Herdr.AgentRead(ctx, a.Pane, readLines)
	if err != nil {
		return nil, "herdr: " + err.Error()
	}
	all, ok := replyLinks(string(text), id)
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
