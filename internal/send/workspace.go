package send

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
)

// ProjectWorkspace is the herdr workspace of repo's project (spec §9.2). Its checkout comes
// from projects, by convention or override (spec §3); the workspace from herdr's live state,
// the first of:
//
//  1. the workspace labelled as the project's override says;
//  2. the one workspace with a pane whose cwd is the checkout or under it;
//  3. the workspace labelled with the repo's name;
//  4. none of these: a workspace created at the checkout, labelled with the override, else
//     with the repo's name, without focus.
//
// root is the created workspace's initial shell pane, "" when one was found. A missing
// checkout is an error, and nothing is cloned. Send and dispatch both resolve here; the tool
// never closes a workspace it created.
func ProjectWorkspace(ctx context.Context, c herdr.Client, projects config.Projects, repo string) (ws Workspace, root string, err error) {
	p := projects.Project(repo)
	spaces, err := c.WorkspaceList(ctx)
	if err != nil {
		return Workspace{}, "", err
	}
	labelled := func(label string) (Workspace, int) {
		var ids []string
		for _, w := range spaces {
			if w.Label == label && !slices.Contains(ids, w.ID) {
				ids = append(ids, w.ID)
			}
		}
		if len(ids) != 1 {
			return Workspace{}, len(ids)
		}
		return Workspace{ID: ids[0], Label: label}, 1
	}

	if p.Workspace != "" {
		switch ws, n := labelled(p.Workspace); n {
		case 0:
			return create(ctx, c, repo, p.Path, p.Workspace)
		case 1:
			return ws, "", nil
		default:
			return Workspace{}, "", fmt.Errorf("projects.%q.workspace: %d herdr workspaces labelled %q", repo, n, p.Workspace)
		}
	}

	if p.Path != "" {
		panes, err := c.PaneList(ctx)
		if err != nil {
			return Workspace{}, "", err
		}
		var at []Workspace
		for _, pn := range panes {
			if !Within(p.Path, pn.Cwd) || slices.ContainsFunc(at, func(w Workspace) bool { return w.ID == pn.Workspace }) {
				continue
			}
			ws := Workspace{ID: pn.Workspace, Label: pn.Workspace}
			if i := slices.IndexFunc(spaces, func(w Workspace) bool { return w.ID == pn.Workspace }); i >= 0 {
				ws.Label = spaces[i].Label
			}
			at = append(at, ws)
		}
		switch len(at) {
		case 0:
		case 1:
			return at[0], "", nil
		default:
			names := make([]string, len(at))
			for i, w := range at {
				names[i] = fmt.Sprintf("%q (%s)", w.Label, w.ID)
			}
			return Workspace{}, "", fmt.Errorf("herdr workspaces %s each have a pane in %s: set projects.%q.workspace to the project's",
				strings.Join(names, ", "), p.Path, repo)
		}
	}

	_, name, _ := strings.Cut(repo, "/")
	switch ws, n := labelled(name); n {
	case 0:
		return create(ctx, c, repo, p.Path, name)
	case 1:
		return ws, "", nil
	default:
		return Workspace{}, "", fmt.Errorf("%d herdr workspaces labelled %q: set projects.%q.workspace to the project's", n, name, repo)
	}
}

// create is §9.2's step 4: a herdr workspace for repo, labelled label, at its checkout, which
// must exist.
func create(ctx context.Context, c herdr.Client, repo, checkout, label string) (Workspace, string, error) {
	if checkout == "" {
		return Workspace{}, "", fmt.Errorf("no herdr workspace for %s, and no checkout to open one at: set projects.root, or projects.%q.path", repo, repo)
	}
	if fi, err := os.Stat(checkout); err != nil || !fi.IsDir() {
		return Workspace{}, "", fmt.Errorf("no herdr workspace for %s, and no checkout of it at %s to open one at; nothing is cloned: clone it there, or set projects.%q.path",
			repo, checkout, repo)
	}
	root, err := c.WorkspaceCreate(ctx, checkout, label)
	if err != nil {
		return Workspace{}, "", err
	}
	return Workspace{ID: root.Workspace, Label: label}, root.ID, nil
}
