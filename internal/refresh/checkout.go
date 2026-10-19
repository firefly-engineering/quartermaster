package refresh

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// removeCheckout removes ws, `<workspace_root>/<owner>/<repo>/<name>`, a finished fresh
// dispatch's checkout, from the project of owner/repo, as dispatch made it there (ADR 0004):
// with jj when the project has .jj, else with git. It returns "" once ws is removed, or found
// gone, else why it stays, to be retried by the next refresh.
func (g *gatherer) removeCheckout(ctx context.Context, ws string) (reason string) {
	repo := filepath.Base(filepath.Dir(filepath.Dir(ws))) + "/" + filepath.Base(filepath.Dir(ws))
	project := g.Projects.Project(repo).Path
	if project == "" {
		return fmt.Sprintf("removing checkout %s: neither projects.root nor projects.%q.path is set; retried next refresh", ws, repo)
	}
	var err error
	if fi, statErr := os.Stat(filepath.Join(project, ".jj")); statErr == nil && fi.IsDir() {
		err = g.jjRemove(ctx, project, ws)
	} else {
		err = g.gitRemove(ctx, project, ws)
	}
	if err != nil {
		return fmt.Sprintf("removing checkout %s: %v; retried next refresh", ws, err)
	}
	return ""
}

// jjRemove snapshots ws, stale or not, so its last edits are in a commit, forgets it in
// project, and deletes it. The workspace's name is ws's base, as dispatch names it. Forgetting
// a workspace already forgotten is not an error, so a deletion that failed is retried whole.
func (g *gatherer) jjRemove(ctx context.Context, project, ws string) error {
	there, err := exists(ws)
	if err != nil {
		return err
	}
	if there {
		// A workspace whose commit was rewritten elsewhere, as linearising a stack does, is
		// stale: update-stale first snapshots its edits into a commit of their own. On one not
		// stale it does nothing.
		if _, err := g.JJ(ctx, "-R", ws, "workspace", "update-stale"); err != nil {
			return err
		}
		if _, err := g.JJ(ctx, "-R", ws, "status"); err != nil {
			return err
		}
	}
	// The forget needs no snapshot of the project's own working copy.
	if _, err := g.JJ(ctx, "-R", project, "--ignore-working-copy", "workspace", "forget", filepath.Base(ws)); err != nil {
		return err
	}
	return os.RemoveAll(ws)
}

// gitRemove removes the worktree ws of project, never forced: git refuses a dirty one. A
// worktree whose directory is gone is pruned.
func (g *gatherer) gitRemove(ctx context.Context, project, ws string) error {
	there, err := exists(ws)
	if err != nil {
		return err
	}
	if !there {
		_, err = g.Git(ctx, "-C", project, "worktree", "prune")
		return err
	}
	_, err = g.Git(ctx, "-C", project, "worktree", "remove", ws)
	return err
}

// exists reports whether anything is at path.
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
