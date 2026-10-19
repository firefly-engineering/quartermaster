package dispatch

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/send"
)

// herdrWorkspace is the herdr workspace of repo's project (spec §9.2). Its checkout comes from
// the configuration, by convention or override (spec §3); the workspace from herdr's live
// state, the first of:
//
//  1. the workspace labelled as the project's override says;
//  2. the one workspace with a pane whose cwd is the checkout or under it;
//  3. the workspace labelled with the repo's name.
func (d Dispatcher) herdrWorkspace(ctx context.Context, repo string) (send.Workspace, error) {
	p := d.Config.Projects.Project(repo)
	spaces, err := d.herdr().WorkspaceList(ctx)
	if err != nil {
		return send.Workspace{}, err
	}
	labelled := func(label string) (send.Workspace, int) {
		var ids []string
		for _, w := range spaces {
			if w.Label == label && !slices.Contains(ids, w.ID) {
				ids = append(ids, w.ID)
			}
		}
		if len(ids) != 1 {
			return send.Workspace{}, len(ids)
		}
		return send.Workspace{ID: ids[0], Label: label}, 1
	}

	if p.Workspace != "" {
		ws, n := labelled(p.Workspace)
		if n != 1 {
			return ws, fmt.Errorf("projects.%q.workspace: %d herdr workspaces labelled %q", repo, n, p.Workspace)
		}
		return ws, nil
	}

	if p.Path != "" {
		panes, err := d.herdr().PaneList(ctx)
		if err != nil {
			return send.Workspace{}, err
		}
		var at []send.Workspace
		for _, pn := range panes {
			if !send.Within(p.Path, pn.Cwd) || slices.ContainsFunc(at, func(w send.Workspace) bool { return w.ID == pn.Workspace }) {
				continue
			}
			ws := send.Workspace{ID: pn.Workspace, Label: pn.Workspace}
			if i := slices.IndexFunc(spaces, func(w send.Workspace) bool { return w.ID == pn.Workspace }); i >= 0 {
				ws.Label = spaces[i].Label
			}
			at = append(at, ws)
		}
		switch len(at) {
		case 0:
		case 1:
			return at[0], nil
		default:
			names := make([]string, len(at))
			for i, w := range at {
				names[i] = fmt.Sprintf("%q (%s)", w.Label, w.ID)
			}
			return send.Workspace{}, fmt.Errorf("herdr workspaces %s each have a pane in %s: set projects.%q.workspace to the project's",
				strings.Join(names, ", "), p.Path, repo)
		}
	}

	_, name, _ := strings.Cut(repo, "/")
	switch ws, n := labelled(name); n {
	case 1:
		return ws, nil
	case 0:
		return ws, fmt.Errorf("no herdr workspace for %s: none has a pane in %q or is labelled %q; set projects.%q.workspace",
			repo, p.Path, name, repo)
	default:
		return ws, fmt.Errorf("%d herdr workspaces labelled %q: set projects.%q.workspace to the project's", n, name, repo)
	}
}
