package refresh

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/firefly-engineering/quartermaster/internal/dispatch"
	"github.com/firefly-engineering/quartermaster/internal/rex"
)

// worker asks rex about the pane a dispatched item's agent was started in. A pane that is
// gone, or that no agent occupies any more, is a worker that exited: it returns the WAITING
// summary. Otherwise it returns why the item stays as it is.
func (g *gatherer) worker(ctx context.Context, pane string) (waiting, reason string) {
	p, err := g.Rex.PaneGet(ctx, pane)
	switch {
	case rex.Code(err) == rex.PaneNotFound:
		return "worker exited: pane " + pane + " is gone", ""
	case err != nil:
		return "", "rex: " + err.Error()
	case p.Kind == "":
		return "worker exited: no agent in pane " + pane, ""
	}
	return "", fmt.Sprintf("its worker %s in pane %s is %s", p.Kind, pane, p.Status)
}

// closeTab closes the tab of pane, where a fresh dispatch in workspace ws started its
// agent, under dispatch.CloseTab's guards. It returns the tab it closed, "" when the pane or its
// tab was gone already; or why the pane stays, for this refresh, or for good when its agent was
// renamed.
func (g *gatherer) closeTab(ctx context.Context, pane, ws string) (tab, reason string) {
	tab, err := dispatch.CloseTab(ctx, g.Rex, g.agents, pane, ws)
	var kept *dispatch.Kept
	switch {
	case errors.As(err, &kept):
		return "", err.Error()
	case err != nil:
		return "", err.Error() + "; retried next refresh"
	}
	return tab, ""
}

type agent = rex.Agent

// agents lists rex's agents, once per refresh.
func (g *gatherer) agents(ctx context.Context) ([]agent, error) {
	if !g.agentsRead {
		g.agentList, g.agentsErr = g.Rex.AgentList(ctx)
		g.agentsRead = true
	}
	return g.agentList, g.agentsErr
}

// readLines is how much of an agent's recent output refresh reads for a reply.
const readLines = 400

// reply reads the agent in pane, which the send of item id's prompt recorded, for its reply to
// the prompt. It returns the reply's GitHub links not in the item's line already, or why there
// are none to record.
func (g *gatherer) reply(ctx context.Context, id, pane, title string) (links []string, reason string) {
	if pane == "" {
		return nil, "no pane recorded to read a reply from"
	}
	who := "pane " + pane
	agents, err := g.agents(ctx)
	if err != nil {
		return nil, "rex: " + err.Error()
	}
	i := slices.IndexFunc(agents, func(a agent) bool { return a.Pane == pane })
	if i < 0 {
		return nil, "rex: no agent in " + who
	}
	a := agents[i]
	if a.Status != rex.Idle && a.Status != rex.Done {
		return nil, fmt.Sprintf("the agent in %s is %s", who, a.Status)
	}
	text, err := g.Rex.AgentRead(ctx, a.Pane, readLines)
	if err != nil {
		return nil, "rex: " + err.Error()
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
