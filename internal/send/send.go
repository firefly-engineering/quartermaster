// Package send sends one work item's prompt from the day's note to its herdr session: the
// ▶ button handler (spec §9.1).
//
// Any web page can open the button's obsidian:// URI, so the only inputs are a date and an
// item id, both validated. The prompt text comes from the user's own note, and reaches
// herdr as an argument, never through a shell.
package send

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// sendable are the agent statuses a prompt may be typed into. A blocked agent shows an
// approval or question UI, which typed text could answer.
var sendable = []string{"idle", "done", "working"}

// Sender runs herdr. The caller resolves both fields; nothing here reads the environment.
type Sender struct {
	Herdr string   // the herdr executable's path; "" when it was not found
	Env   []string // herdr's environment
}

// Sent is a prompt delivered to a herdr pane.
type Sent struct {
	ID, To, Pane string
	Path         string // the note the closing line names
}

func (s Sent) String() string { return fmt.Sprintf("sent %s to %s (%s)", s.ID, s.To, s.Pane) }

// Send sends work item id's prompt from vault/<date>.md to the one agent in the herdr
// workspace its block names.
func (s Sender) Send(ctx context.Context, vault, date, id string) (Sent, error) {
	return s.SendItem(ctx, vault, date, id, "")
}

// SendItem is Send with extra, when not blank, added after the prompt as its own paragraph:
// task_dispatch's instruction.
func (s Sender) SendItem(ctx context.Context, vault, date, id, extra string) (Sent, error) {
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return Sent{}, fmt.Errorf("not a date: %q", date)
	}
	if !note.ValidID(id) {
		return Sent{}, fmt.Errorf("not an item id: %q", id)
	}
	path := filepath.Join(vault, date+".md")
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return Sent{}, fmt.Errorf("no note %s", path)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return Sent{}, err
	}
	to, text, err := prompt(src, id)
	if err != nil {
		return Sent{}, err
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		text += "\n\n" + extra
	}
	sent, err := s.deliver(ctx, to, text, Item(id), path)
	sent.ID = id
	return sent, err
}

// Deliver sends text to the one agent in the herdr workspace labelled to, closed with the line
// asking it to update what (Closing), in the note at path. Sent.ID is what. It is for a prompt
// no herdr-prompt block holds; the agent's status is checked as Send checks it.
func (s Sender) Deliver(ctx context.Context, to, text, what, path string) (Sent, error) {
	text = strings.TrimSpace(text)
	if to == "" || text == "" {
		return Sent{}, fmt.Errorf("a prompt needs a workspace and a text: to %q, text %q", to, text)
	}
	return s.deliver(ctx, to, text, what, path)
}

// Closing is the line that ends every prompt sent, asking the agent to record its outcome on
// what, as Item(id) or `owner/repo#N`, in the note at path.
func Closing(what, path string) string {
	return "When this is done, update the worklog with the worklog skill: " + what + " in " + path +
		". Record the outcome and refresh Next."
}

// deliver sends text and the closing line about what to the agent of workspace to.
func (s Sender) deliver(ctx context.Context, to, text, what, path string) (Sent, error) {
	pane, status, err := s.resolve(ctx, to)
	if err != nil {
		return Sent{}, err
	}
	if !slices.Contains(sendable, status) {
		return Sent{}, fmt.Errorf("%s agent is %s; answer it first", to, status)
	}
	text += "\n\n" + Closing(what, path)
	if _, err := s.herdr(ctx, "agent", "prompt", pane, text); err != nil {
		return Sent{}, err
	}
	return Sent{ID: what, To: to, Pane: pane, Path: path}, nil
}

// Item names work item id in the closing line, as Closing's what: `work item ^<id>`.
func Item(id string) string { return "work item ^" + id }

// PromptTag is how the closing line names work item id, just before the note's path: the part
// of Closing(Item(id), path) before the path. refresh finds an agent's reply to the prompt
// after it.
func PromptTag(id string) string { return Item(id) + " in " }

// prompt returns the workspace label and text of item id's herdr-prompt block. The note need
// not parse cleanly elsewhere; a parse error is reported only when the block is not found.
func prompt(src []byte, id string) (to, text string, err error) {
	n, perr := note.Parse(src)
	it := n.Item(id)
	if it == nil || it.Prompt == nil {
		err := fmt.Errorf("no herdr-prompt block with id=%q", id)
		if perr != nil {
			err = fmt.Errorf("%w; the note does not parse: %w", err, perr)
		}
		return "", "", err
	}
	text = strings.TrimSpace(dedent(it.Prompt.Body))
	if text == "" {
		return "", "", fmt.Errorf("block %q needs to= and a body", id)
	}
	return it.Prompt.To, text, nil
}

// dedent removes the leading spaces and tabs every non-blank line shares, and empties lines
// of only spaces and tabs, as Python's textwrap.dedent does.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	margin, first := "", true
	for i, l := range lines {
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		if lead == l {
			lines[i] = ""
			continue
		}
		if first {
			margin, first = lead, false
			continue
		}
		k := 0
		for k < len(margin) && k < len(lead) && margin[k] == lead[k] {
			k++
		}
		margin = margin[:k]
	}
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, margin)
	}
	return strings.Join(lines, "\n")
}

// resolve maps a workspace label (one session per project) to the one agent pane in it.
func (s Sender) resolve(ctx context.Context, label string) (pane, status string, err error) {
	var spaces struct {
		Result struct {
			Workspaces []struct {
				ID    string `json:"workspace_id"`
				Label string `json:"label"`
			} `json:"workspaces"`
		} `json:"result"`
	}
	if err := s.herdrJSON(ctx, &spaces, "workspace", "list"); err != nil {
		return "", "", err
	}
	ids := map[string]bool{}
	for _, w := range spaces.Result.Workspaces {
		if w.Label == label {
			ids[w.ID] = true
		}
	}
	if len(ids) != 1 {
		return "", "", fmt.Errorf("%d herdr workspaces labelled %q", len(ids), label)
	}
	var agents struct {
		Result struct {
			Agents []struct {
				Workspace string `json:"workspace_id"`
				Pane      string `json:"pane_id"`
				Status    string `json:"agent_status"`
			} `json:"agents"`
		} `json:"result"`
	}
	if err := s.herdrJSON(ctx, &agents, "agent", "list"); err != nil {
		return "", "", err
	}
	var in []int
	for i, a := range agents.Result.Agents {
		if ids[a.Workspace] {
			in = append(in, i)
		}
	}
	if len(in) != 1 {
		return "", "", fmt.Errorf("%d agents in workspace %q; expected one", len(in), label)
	}
	a := agents.Result.Agents[in[0]]
	return a.Pane, a.Status, nil
}

func (s Sender) herdrJSON(ctx context.Context, v any, args ...string) error {
	out, err := s.herdr(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("herdr %s: %w", strings.Join(args[:2], " "), err)
	}
	return nil
}

// herdr runs herdr with args, directly, with no shell.
func (s Sender) herdr(ctx context.Context, args ...string) ([]byte, error) {
	if s.Herdr == "" {
		return nil, errors.New("herdr not found on PATH")
	}
	cmd := exec.CommandContext(ctx, s.Herdr, args...)
	cmd.Env = s.Env
	if cmd.Env == nil {
		cmd.Env = []string{} // nil would hand herdr this process's environment
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("herdr %s: %s", strings.Join(args[:2], " "), msg)
	}
	if err != nil {
		return nil, fmt.Errorf("herdr: %w", err)
	}
	return stdout.Bytes(), nil
}
