// Package engine is the only writer of a worklog note. Every mutation runs the sequence of
// spec §7 (ADR 0006): lock, read and hash, splice, write a temp file, re-hash, rename, commit.
package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// DefaultLockTimeout is how long Mutate waits for the note's lock when Engine.LockTimeout is 0.
const DefaultLockTimeout = 10 * time.Second

// maxRetries is how many times a mutation starts over when the note changed under it.
const maxRetries = 3

// ignoreEntry is the .gitignore line that keeps the lock directory out of jj (spec §7).
const ignoreEntry = ".quartermaster/"

// Engine writes the notes of one vault. The caller resolves every field: nothing here reads the
// environment or the cwd.
type Engine struct {
	Repo        string        // the repo holding the vault (vault.repo), absolute
	Dir         string        // the Obsidian vault, relative to Repo (vault.dir)
	JJ          string        // jj's path; needed only when Repo is a jj repo
	Env         []string      // jj's environment
	LockTimeout time.Duration // 0 is DefaultLockTimeout

	// beforeCheck, when set, runs after the temp file is written and before the note is
	// re-hashed, with the 0-based attempt: the tests' stand-in for Obsidian writing.
	beforeCheck func(attempt int)
}

// Edit changes a note. n is the parsed note and src its source, both nil when the note does
// not exist yet or is empty. It returns the note's new source, spliced from src (ADR 0002),
// and a one-line headline for the commit message. Mutate may run it more than once, on a
// fresh read each time, so it must not act outside the note.
type Edit func(n *note.Note, src []byte) (out []byte, headline string, err error)

// Result is a mutation's outcome.
type Result struct {
	Path      string     // the note's path
	Note      *note.Note // the note as it now is
	Changed   bool       // false when the edit left the note as it was, and nothing was written
	Committed bool       // the note was committed as one jj change
	Skipped   string     // why the note was not committed, when it changed but was not
}

// The errors Mutate wraps.
var (
	ErrLockTimeout    = errors.New("timed out waiting for the note's lock")
	ErrConcurrentEdit = errors.New("the note kept changing under the mutation")
	ErrUnparseable    = errors.New("the note does not parse")
	ErrUntick         = errors.New("a write may not untick a checkbox")
)

// Mutate is the only path that writes a note. Under the note's lock it reads the note of date,
// runs edit on it, settles the result (reconciled *Next* pairs, derived icons, no box
// unticked), and renames it over the note unless the note changed meanwhile, in which case it
// starts over, at most maxRetries times. In a jj repo it then commits the note alone as
// `worklog(<date>): <tool>: <headline>`. A note that does not parse is never written.
func (e *Engine) Mutate(ctx context.Context, date, tool string, edit Edit) (Result, error) {
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return Result{}, fmt.Errorf("not a date: %q", date)
	}
	if !oneLine(tool) || strings.ContainsAny(tool, " \t") {
		return Result{}, fmt.Errorf("not a tool name: %q", tool)
	}
	if !filepath.IsAbs(e.Repo) {
		return Result{}, fmt.Errorf("vault repo %q is not an absolute path", e.Repo)
	}
	vault := filepath.Join(e.Repo, e.Dir)
	notePath := filepath.Join(vault, date+".md")
	isJJ := isDir(filepath.Join(e.Repo, ".jj"))
	if isJJ && e.JJ == "" {
		return Result{}, fmt.Errorf("%s is a jj repo, but jj was not found", e.Repo)
	}
	timeout := e.LockTimeout
	if timeout == 0 {
		timeout = DefaultLockTimeout
	}

	// The lock directory must be ignored before it exists, or a jj snapshot would track it.
	if err := e.ensureIgnored(ctx, isJJ, timeout); err != nil {
		return Result{}, err
	}
	unlock, err := lock(ctx, filepath.Join(vault, ".quartermaster", "locks", date+".lock"), timeout)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	res := Result{Path: notePath}
	var headline string
	for attempt := 0; ; attempt++ {
		src, existed, err := readNote(notePath)
		if err != nil {
			return Result{}, err
		}
		var was *note.Note
		if len(src) > 0 {
			if was, err = parse(src, notePath); err != nil {
				return Result{}, err
			}
		} else {
			src = nil
		}
		var out []byte
		out, headline, err = edit(was, src)
		if err != nil {
			return Result{}, err
		}
		if strings.TrimSpace(headline) == "" || !oneLine(headline) {
			return Result{}, fmt.Errorf("not a one-line headline: %q", headline)
		}
		out, res.Note, err = settle(was, out)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", notePath, err)
		}
		if existed && bytes.Equal(out, src) {
			return res, nil
		}
		tmp, err := writeTemp(vault, date, out)
		if err != nil {
			return Result{}, err
		}
		if e.beforeCheck != nil {
			e.beforeCheck(attempt)
		}
		now, exists, err := readNote(notePath)
		if err != nil {
			_ = os.Remove(tmp)
			return Result{}, err
		}
		if exists != existed || sha256.Sum256(now) != sha256.Sum256(src) {
			_ = os.Remove(tmp)
			if attempt == maxRetries {
				return Result{}, fmt.Errorf("%s: %w", notePath, ErrConcurrentEdit)
			}
			continue
		}
		if err := os.Rename(tmp, notePath); err != nil {
			_ = os.Remove(tmp)
			return Result{}, err
		}
		res.Changed = true
		break
	}
	if err := syncDir(vault); err != nil {
		return res, err
	}

	if !isJJ {
		res.Skipped = e.Repo + " is not a jj repo"
		return res, nil
	}
	rel := filepath.ToSlash(filepath.Join(e.Dir, date+".md"))
	msg := fmt.Sprintf("worklog(%s): %s: %s", date, tool, strings.TrimSpace(headline))
	if err := e.commit(ctx, timeout, msg, rel); err != nil {
		return res, err
	}
	res.Committed = true
	return res, nil
}

// ensureIgnored adds ignoreEntry to the repo's .gitignore when it lacks it, and commits that
// as its own jj change. It holds a flock on .gitignore meanwhile, so the entry is added once.
func (e *Engine) ensureIgnored(ctx context.Context, isJJ bool, timeout time.Duration) error {
	gi := filepath.Join(e.Repo, ".gitignore")
	if b, err := os.ReadFile(gi); err == nil && ignores(b, e.Dir) {
		return nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(gi, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := flock(ctx, f, timeout); err != nil {
		return fmt.Errorf("%s: %w", gi, err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if ignores(b, e.Dir) { // another process added it while this one waited
		return nil
	}
	add := ignoreEntry + "\n"
	if len(b) > 0 && b[len(b)-1] != '\n' {
		add = "\n" + add
	}
	if _, err := f.Write([]byte(add)); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if !isJJ {
		return nil
	}
	return e.commit(ctx, timeout, "chore(vault): ignore "+ignoreEntry, ".gitignore")
}

// ignores reports whether a .gitignore has a line ignoring the vault's .quartermaster: at any
// depth, or anchored at the vault dir.
func ignores(gitignore []byte, dir string) bool {
	anchored := "/" + path.Join(filepath.ToSlash(dir), ignoreEntry)
	for line := range strings.Lines(string(gitignore)) {
		line = strings.TrimSuffix(strings.TrimSpace(line), "/") + "/"
		if line == ignoreEntry || line == "**/"+ignoreEntry || line == anchored+"/" {
			return true
		}
	}
	return false
}

// commit runs `jj commit` on the repo-relative path rel alone, with no shell in between. The
// note locks are per date but jj's working copy is the repo's, so commits take a repo-wide
// lock of their own. Whatever else `@` holds stays in the new `@`; a description `@` had is
// replaced by msg, so the vault repo is expected to keep `@` undescribed.
func (e *Engine) commit(ctx context.Context, timeout time.Duration, msg, rel string) error {
	unlock, err := lock(ctx, filepath.Join(e.Repo, e.Dir, ".quartermaster", "locks", "jj.lock"), timeout)
	if err != nil {
		return err
	}
	defer unlock()
	cmd := exec.CommandContext(ctx, e.JJ, "-R", e.Repo, "commit", "-m", msg, fileset(rel))
	cmd.Env = e.Env
	if cmd.Env == nil {
		cmd.Env = []string{} // nil would hand jj this process's environment
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("jj commit %s: %w: %s", rel, err, bytes.TrimSpace(out))
	}
	return nil
}

// fileset is the jj fileset naming exactly the repo-relative path rel.
func fileset(rel string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `root:"` + r.Replace(path.Clean(rel)) + `"`
}

// readNote reads the note, and whether it exists.
func readNote(p string) ([]byte, bool, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// writeTemp writes src to a new `<date>.md.tmp.<random>` in dir, fsynced, and returns its path.
func writeTemp(dir, date string, src []byte) (string, error) {
	f, err := os.CreateTemp(dir, date+".md.tmp.*")
	if err != nil {
		return "", err
	}
	_, err = f.Write(src)
	if err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// syncDir fsyncs a directory, so a rename in it is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func oneLine(s string) bool { return s != "" && !strings.ContainsAny(s, "\r\n") }
