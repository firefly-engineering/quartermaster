// Package command runs an external executable directly, with no shell, for herdr's client,
// refresh's gh and dispatch's jj and git.
//
// Like every package but main, this one reads neither the environment nor the cwd: the caller
// gives the path, the directory and the whole environment.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run runs the executable at path with args, in dir ("" for this process's), and with env as
// its whole environment, and returns its standard output. A nil env is an empty one, never this
// process's. A run that exits non-zero returns an *ExitError; one that never started (path
// absent, dir missing) returns exec's error. A path with no separator, as "" or "gh", is
// refused rather than looked up on this process's PATH. Each caller names the call in its own
// error text.
//
// Engine.commit keeps its own nil-env guard rather than calling Run: it reports jj's combined
// output through exec's own error, a text Run's split streams would change.
func Run(ctx context.Context, path, dir string, env, args []string) ([]byte, error) {
	if !strings.ContainsRune(path, filepath.Separator) {
		return nil, fmt.Errorf("%q is not a path: Run looks nothing up on PATH", path)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = env
	if cmd.Env == nil {
		cmd.Env = []string{} // nil would hand the child this process's environment
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
		return nil, &ExitError{Args: args, Stderr: stderr.Bytes(), Stdout: stdout.Bytes()}
	}
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// ExitError is a run that exited non-zero, with its args and what it wrote.
type ExitError struct {
	Args           []string
	Stderr, Stdout []byte
}

func (e *ExitError) Error() string { return "exited non-zero: " + e.Message() }

// Message is what the run wrote to standard error, else to standard output, trimmed.
func (e *ExitError) Message() string {
	if msg := strings.TrimSpace(string(e.Stderr)); msg != "" {
		return msg
	}
	return strings.TrimSpace(string(e.Stdout))
}
