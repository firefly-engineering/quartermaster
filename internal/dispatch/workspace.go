package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/command"
)

// workspace makes ws, named name, the ref's workspace of project: with `jj workspace add`
// when the project has .jj, else with `git worktree add` (ADR 0004). An existing workspace
// of that name at ws is reused, never clobbered; one elsewhere, or anything else at ws, is
// refused with its path.
func (d Dispatcher) workspace(ctx context.Context, project, ws, name string) (reused bool, err error) {
	if fi, err := os.Stat(filepath.Join(project, ".jj")); err == nil && fi.IsDir() {
		return d.jjWorkspace(ctx, project, ws, name)
	}
	return d.gitWorktree(ctx, project, ws, name)
}

func (d Dispatcher) jjWorkspace(ctx context.Context, project, ws, name string) (bool, error) {
	// Reading needs no snapshot of the project's working copy.
	jj := func(args ...string) (string, error) {
		return d.run(ctx, "jj", d.JJ, project, append([]string{"-R", project, "--ignore-working-copy"}, args...)...)
	}
	out, err := jj("workspace", "list", "-T", `name ++ "\n"`)
	if err != nil {
		return false, err
	}
	if slices.Contains(strings.Split(out, "\n"), name) {
		at, err := jj("workspace", "root", "--name", name)
		if err != nil {
			return false, err
		}
		if at = strings.TrimSpace(at); samePath(at, ws) {
			if fi, err := os.Stat(ws); err != nil || !fi.IsDir() {
				return false, fmt.Errorf("jj workspace %s of %s is at %s, which is gone: run `jj workspace forget %s` in %s first",
					name, project, ws, name, project)
			}
			return true, nil
		}
		return false, fmt.Errorf("jj workspace %s of %s is at %s, not %s: forget or rename it first", name, project, at, ws)
	}
	if err := free(ws); err != nil {
		return false, err
	}
	_, err = d.run(ctx, "jj", d.JJ, project, "-R", project, "workspace", "add", "--name", name, ws)
	return false, err
}

func (d Dispatcher) gitWorktree(ctx context.Context, project, ws, name string) (bool, error) {
	git := func(args ...string) (string, error) {
		return d.run(ctx, "git", d.Git, project, append([]string{"-C", project}, args...)...)
	}
	// A worktree whose directory is gone would still hold its path and branch.
	if _, err := git("worktree", "prune"); err != nil {
		return false, err
	}
	out, err := git("worktree", "list", "--porcelain", "-z")
	if err != nil {
		return false, err
	}
	branch := "refs/heads/" + name
	for _, w := range worktrees(out) {
		switch {
		case samePath(w.path, ws) && w.branch == branch:
			return true, nil
		case samePath(w.path, ws):
			return false, fmt.Errorf("%s is a worktree of %s, not on branch %s", ws, project, name)
		case w.branch == branch:
			return false, fmt.Errorf("branch %s of %s is checked out at %s, not %s: remove that worktree first", name, project, w.path, ws)
		}
	}
	if err := free(ws); err != nil {
		return false, err
	}
	if _, err := git("rev-parse", "--verify", "--quiet", branch); err == nil {
		_, err = git("worktree", "add", ws, name)
		return false, err
	}
	_, err = git("worktree", "add", "-b", name, ws)
	return false, err
}

type worktree struct{ path, branch string }

// worktrees reads `git worktree list --porcelain -z`: NUL-terminated `<key> <value>` lines,
// each worktree's ended by an empty one.
func worktrees(out string) []worktree {
	var ws []worktree
	for _, line := range strings.Split(out, "\x00") {
		k, v, _ := strings.Cut(line, " ")
		switch k {
		case "worktree":
			ws = append(ws, worktree{path: v})
		case "branch":
			if len(ws) > 0 {
				ws[len(ws)-1].branch = v
			}
		}
	}
	return ws
}

// free makes ws's parent and checks nothing is at ws itself.
func free(ws string) error {
	if _, err := os.Lstat(ws); err == nil {
		return fmt.Errorf("%s exists and is not the ref's workspace: move it away first", ws)
	}
	return os.MkdirAll(filepath.Dir(ws), 0o755)
}

// samePath reports whether a and b name one directory, through symlinks.
func samePath(a, b string) bool {
	// A gone path resolves through its deepest existing parent, as /var is /private/var on macOS.
	var resolve func(p string) string
	resolve = func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		if dir := filepath.Dir(p); dir != p {
			return filepath.Join(resolve(dir), filepath.Base(p))
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// run runs bin, named what in errors, with args in dir, directly, with no shell, and returns
// its stdout.
func (d Dispatcher) run(ctx context.Context, what, bin, dir string, args ...string) (string, error) {
	if bin == "" {
		return "", fmt.Errorf("%s not found on PATH", what)
	}
	out, err := command.Run(ctx, bin, dir, d.Env, args)
	if exit := (*command.ExitError)(nil); errors.As(err, &exit) {
		return "", fmt.Errorf("%s %s: %s", what, strings.Join(args[:min(len(args), 4)], " "), exit.Message())
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	return string(out), nil
}
