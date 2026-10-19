// Package send sends one work item's prompt from the day's note to its herdr session: the
// ▶ button handler (spec §9.1).
//
// Any web page can open the button's obsidian:// URI, so the only inputs are a date and an
// item id, both validated. The prompt text comes from the user's own note, and reaches
// herdr as an argument, never through a shell.
package send

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/herdr"
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
	ws, err := s.labelled(ctx, to)
	if err != nil {
		return Sent{}, err
	}
	sent, err := s.deliver(ctx, ws, text, Item(id), path)
	sent.ID = id
	return sent, err
}

// Workspace is one herdr workspace: its id, and the label messages and Sent.To name it by.
type Workspace = herdr.Workspace

// Deliver sends text to the one agent in herdr workspace to, closed with the line asking it to
// update what (Closing), in the note at path. Sent.ID is what. It is for a prompt no
// herdr-prompt block holds; the agent's status is checked as Send checks it.
func (s Sender) Deliver(ctx context.Context, to Workspace, text, what, path string) (Sent, error) {
	text = strings.TrimSpace(text)
	if to.ID == "" || text == "" {
		return Sent{}, fmt.Errorf("a prompt needs a workspace and a text: to %q, text %q", to.ID, text)
	}
	if to.Label == "" {
		to.Label = to.ID
	}
	return s.deliver(ctx, to, text, what, path)
}

// Closing is the line that ends every prompt sent, asking the agent to record its outcome on
// what, as Item(id) or `owner/repo#N`, in the note at path.
func Closing(what, path string) string {
	return ClosingLead + what + " in " + path + ". Record the outcome and refresh Next."
}

// ClosingLead starts every closing line (Closing), whatever it names: refresh ends a reply at
// the next one.
const ClosingLead = "When this is done, update the worklog with the worklog skill: "

// deliver sends text and the closing line about what to the agent of workspace to.
func (s Sender) deliver(ctx context.Context, to Workspace, text, what, path string) (Sent, error) {
	pane, status, err := s.agent(ctx, to)
	if err != nil {
		return Sent{}, err
	}
	if !slices.Contains(sendable, status) {
		return Sent{}, fmt.Errorf("%s agent is %s; answer it first", to.Label, status)
	}
	text += "\n\n" + Closing(what, path)
	if err := s.herdr().AgentPrompt(ctx, pane, text); err != nil {
		return Sent{}, err
	}
	return Sent{ID: what, To: to.Label, Pane: pane, Path: path}, nil
}

// Item names work item id in the closing line, as Closing's what: `work item ^<id>`.
func Item(id string) string { return "work item ^" + id }

// PromptTag is the start of the closing line about work item id, up to the note's path: the part
// of Closing(Item(id), path) before the path. refresh finds an agent's reply to the prompt
// after it.
func PromptTag(id string) string { return ClosingLead + Item(id) + " in " }

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
	text = Text(it.Prompt)
	if text == "" {
		return "", "", fmt.Errorf("block %q needs to= and a body", id)
	}
	return it.Prompt.To, text, nil
}

// Text is the text p sends: its body, dedented and trimmed.
func Text(p *note.Prompt) string { return strings.TrimSpace(dedent(p.Body)) }

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

// labelled is the one herdr workspace labelled label (one session per project).
func (s Sender) labelled(ctx context.Context, label string) (Workspace, error) {
	spaces, err := s.herdr().WorkspaceList(ctx)
	if err != nil {
		return Workspace{}, err
	}
	var ids []string
	for _, w := range spaces {
		if w.Label == label && !slices.Contains(ids, w.ID) {
			ids = append(ids, w.ID)
		}
	}
	if len(ids) != 1 {
		return Workspace{}, fmt.Errorf("%d herdr workspaces labelled %q", len(ids), label)
	}
	return Workspace{ID: ids[0], Label: label}, nil
}

// agent is the one agent pane in workspace ws, and its status.
func (s Sender) agent(ctx context.Context, ws Workspace) (pane, status string, err error) {
	agents, err := s.herdr().AgentList(ctx)
	if err != nil {
		return "", "", err
	}
	var in []int
	for i, a := range agents {
		if a.Workspace == ws.ID {
			in = append(in, i)
		}
	}
	if len(in) != 1 {
		return "", "", fmt.Errorf("%d agents in workspace %q; expected one", len(in), ws.Label)
	}
	a := agents[in[0]]
	return a.Pane, a.Status, nil
}

func (s Sender) herdr() herdr.Client { return herdr.New(s.Herdr, s.Env) }
