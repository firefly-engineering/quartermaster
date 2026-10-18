package refresh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs one external command, gh or herdr, with args and returns its standard output.
// A run that exits non-zero returns an *ExitError. Tests stand in a fake for it.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// ExitError is a command that exited non-zero, with what it wrote to standard error: herdr
// writes its errors there as JSON.
type ExitError struct {
	Name   string // the command, as `herdr`
	Args   []string
	Stderr []byte
	Stdout []byte
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(string(e.Stderr))
	if msg == "" {
		msg = strings.TrimSpace(string(e.Stdout))
	}
	return e.Name + " " + strings.Join(e.Args[:min(2, len(e.Args))], " ") + ": " + msg
}

// Command is a Runner for the executable at path, run directly with no shell and with env as
// its whole environment. A path of "" is a command that was not found, which every run reports.
func Command(name, path string, env []string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if path == "" {
			return nil, fmt.Errorf("%s not found on PATH", name)
		}
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = env
		if cmd.Env == nil {
			cmd.Env = []string{} // nil would hand the command this process's environment
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, &ExitError{Name: name, Args: args, Stderr: stderr.Bytes(), Stdout: stdout.Bytes()}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return stdout.Bytes(), nil
	}
}
