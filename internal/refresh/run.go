package refresh

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/command"
)

// Runner runs one external command, as gh, with args and returns its standard output.
// A run that exits non-zero returns an *ExitError. Tests stand in a fake for it.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// ExitError is a command that exited non-zero, with what it wrote to standard error.
type ExitError struct {
	Name string // the command, as `gh`
	command.ExitError
}

func (e *ExitError) Error() string {
	return e.Name + " " + strings.Join(e.Args[:min(2, len(e.Args))], " ") + ": " + e.Message()
}

// Command is a Runner for the executable at path, run directly with no shell and with env as
// its whole environment. A path of "" is a command that was not found, which every run reports.
func Command(name, path string, env []string) Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if path == "" {
			return nil, fmt.Errorf("%s not found on PATH", name)
		}
		out, err := command.Run(ctx, path, "", env, args)
		if exit := (*command.ExitError)(nil); errors.As(err, &exit) {
			return nil, &ExitError{Name: name, ExitError: *exit}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return out, nil
	}
}
