// Package herdr runs herdr 0.9.5 for send, refresh and dispatch: one typed method per call
// they make, and each of herdr's answers declared once.
//
// Every answer but `agent read`'s, which is the pane's plain text, is JSON `{"id", "result"}`.
// An error is `{"id", "error": {"code", "message"}}` on standard error, with exit status 1.
//
// Like every package but main, this one reads neither the environment nor the cwd: the caller
// gives the executable's path and its environment.
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/command"
)

// Runner runs herdr with args and returns its standard output. A run that exits non-zero
// returns an *ExitError. Tests stand in a fake for it.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Client calls herdr through Run. The zero Client reports herdr as not available.
type Client struct {
	Run Runner
}

// New is a Client for the executable at path, run directly with no shell and with env as its
// whole environment. A path of "" is herdr not found, which every call reports.
func New(path string, env []string) Client {
	return Client{Run: func(ctx context.Context, args ...string) ([]byte, error) {
		if path == "" {
			return nil, errors.New("herdr not found on PATH")
		}
		out, err := command.Run(ctx, path, "", env, args)
		if exit := (*command.ExitError)(nil); errors.As(err, &exit) {
			return nil, (*ExitError)(exit)
		}
		if err != nil {
			return nil, fmt.Errorf("herdr: %w", err)
		}
		return out, nil
	}}
}

// ExitError is a herdr call that exited non-zero, with what it wrote to standard error:
// command's exit error, named for herdr.
type ExitError command.ExitError

func (e *ExitError) Error() string { return "herdr " + verb(e.Args) + ": " + e.Message() }

// Message is what herdr wrote to standard error, else to standard output, trimmed.
func (e *ExitError) Message() string { return (*command.ExitError)(e).Message() }

// PaneNotFound is the error code of a call on a pane that does not exist.
const PaneNotFound = "pane_not_found"

// Code is herdr's error code in err, as PaneNotFound; "" when err is not one of herdr's.
func Code(err error) string {
	var exit *ExitError
	if !errors.As(err, &exit) {
		return ""
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(exit.Stderr, &e) != nil {
		return ""
	}
	return e.Error.Code
}

// Agent is one of `agent list`'s .result.agents.
type Agent struct {
	Name      string `json:"name"` // absent for an agent not started or renamed through herdr
	Workspace string `json:"workspace_id"`
	Pane      string `json:"pane_id"`
	Status    string `json:"agent_status"`
}

// Workspace is one of `workspace list`'s .result.workspaces: its id, and its label.
type Workspace struct {
	ID    string `json:"workspace_id"`
	Label string `json:"label"`
}

// Pane is one of `pane list`'s .result.panes, or `pane get`'s .result.pane.
type Pane struct {
	ID        string `json:"pane_id"`
	Workspace string `json:"workspace_id"`
	Cwd       string `json:"cwd"`
	Agent     string `json:"agent"` // the agent kind in the pane; absent or null for none
	Status    string `json:"agent_status"`
}

// AgentList is `herdr agent list`.
func (c Client) AgentList(ctx context.Context) ([]Agent, error) {
	var r struct {
		Result struct {
			Agents []Agent `json:"agents"`
		} `json:"result"`
	}
	err := c.json(ctx, &r, "agent", "list")
	return r.Result.Agents, err
}

// WorkspaceList is `herdr workspace list`.
func (c Client) WorkspaceList(ctx context.Context) ([]Workspace, error) {
	var r struct {
		Result struct {
			Workspaces []Workspace `json:"workspaces"`
		} `json:"result"`
	}
	err := c.json(ctx, &r, "workspace", "list")
	return r.Result.Workspaces, err
}

// PaneList is `herdr pane list`.
func (c Client) PaneList(ctx context.Context) ([]Pane, error) {
	var r struct {
		Result struct {
			Panes []Pane `json:"panes"`
		} `json:"result"`
	}
	err := c.json(ctx, &r, "pane", "list")
	return r.Result.Panes, err
}

// PaneGet is `herdr pane get <id>`.
func (c Client) PaneGet(ctx context.Context, id string) (Pane, error) {
	var r struct {
		Result struct {
			Pane Pane `json:"pane"`
		} `json:"result"`
	}
	err := c.json(ctx, &r, "pane", "get", id)
	return r.Result.Pane, err
}

// TabCreate is `herdr tab create`, unfocused, in workspace at cwd, labelled label. It returns
// the new tab's pane: .result.root_pane.pane_id.
func (c Client) TabCreate(ctx context.Context, workspace, cwd, label string) (pane string, err error) {
	var r struct {
		Result struct {
			RootPane struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := c.json(ctx, &r, "tab", "create", "--workspace", workspace, "--cwd", cwd, "--label", label, "--no-focus"); err != nil {
		return "", err
	}
	if r.Result.RootPane.ID == "" {
		return "", errors.New("herdr tab create: no root pane in its answer")
	}
	return r.Result.RootPane.ID, nil
}

// AgentStart is `herdr agent start`: an agent of kind, named name, in pane, with args after
// `--` when there are any.
func (c Client) AgentStart(ctx context.Context, name, kind, pane string, args []string) error {
	start := []string{"agent", "start", name, "--kind", kind, "--pane", pane}
	if len(args) > 0 {
		start = append(append(start, "--"), args...)
	}
	_, err := c.run(ctx, start...)
	return err
}

// AgentPrompt is `herdr agent prompt`: text typed into pane's agent.
func (c Client) AgentPrompt(ctx context.Context, pane, text string) error {
	_, err := c.run(ctx, "agent", "prompt", pane, text)
	return err
}

// AgentRead is the last lines of pane's recent output, unwrapped, as plain text: `agent read`
// prints the pane's text, not JSON, and its --format takes only text and ansi.
func (c Client) AgentRead(ctx context.Context, pane string, lines int) ([]byte, error) {
	return c.run(ctx, "agent", "read", pane, "--source", "recent-unwrapped", "--lines", strconv.Itoa(lines), "--format", "text")
}

// json runs herdr with args and decodes its answer into v.
func (c Client) json(ctx context.Context, v any, args ...string) error {
	out, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("herdr %s: %w", verb(args), err)
	}
	return nil
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if c.Run == nil {
		return nil, errors.New("herdr: not available")
	}
	return c.Run(ctx, args...)
}

// verb is the call args make, as `agent list`, which errors name it by.
func verb(args []string) string { return strings.Join(args[:min(2, len(args))], " ") }
