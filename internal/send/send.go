// Package send sends a prompt to a project's session (spec §9.1): the ▶ button handler,
// prompt_send, and task_dispatch's session mode all send through it.
//
// Any web page can open the button's obsidian:// URI, so the only inputs are a date and an
// item id, both validated. The prompt text comes from the user's own note, and reaches
// herdr as an argument, never through a shell. The work item's tier picks a harness through
// config, and the harness's kind picks the agent (ADR 0011). Each send records the agent's
// pane on the item, through the engine.
package send

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/engine"
	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/note"
)

// available are the agent statuses a prompt may be typed into. A working agent would queue
// it behind its turn, and a blocked one shows an approval or question UI that typed text
// could answer.
var available = []string{"idle", "done"}

// tool names the recording of a send's pane in the note's commit message.
const tool = "send"

// Sender sends prompts to sessions. The caller resolves every field; nothing here reads the
// environment.
type Sender struct {
	Config config.Config  // the harnesses, tiers and projects a prompt routes through
	Engine *engine.Engine // the note's only writer, which records each send's pane
	Herdr  string         // the herdr executable's path; "" when it was not found
	Env    []string       // herdr's environment
}

// Sent is a prompt delivered to a herdr pane.
type Sent struct {
	ID, To, Pane string
	Path         string        // the note the closing line names
	Note         engine.Result // the recording of Pane on the work item; zero for a ref with none
}

func (s Sent) String() string { return fmt.Sprintf("sent %s to %s (%s)", s.ID, s.To, s.Pane) }

// Send sends work item id's prompt from vault/<date>.md to an available agent in the herdr
// workspace its block names, and records the agent's pane on the item.
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
	if s.Engine == nil || !sameDir(vault, filepath.Join(s.Engine.Repo, s.Engine.Dir)) {
		return Sent{}, fmt.Errorf("vault %s is not the configured one, vault.repo and vault.dir: a send records its pane there", vault)
	}
	path := filepath.Join(vault, date+".md")
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return Sent{}, fmt.Errorf("no note %s", path)
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return Sent{}, err
	}
	n, it, text, err := prompt(src, id)
	if err != nil {
		return Sent{}, err
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		text += "\n\n" + extra
	}
	ws, err := s.labelled(ctx, it.Prompt.To)
	if err != nil {
		return Sent{}, err
	}
	repo, err := s.project(n, it, it.Prompt.To)
	if err != nil {
		return Sent{}, err
	}
	return s.deliver(ctx, Prompt{
		To: ws, Checkout: s.Config.Projects.Project(repo).Path, Tier: it.Tier, Text: text, Date: date, Item: id,
	})
}

// Workspace is one herdr workspace: its id, and the label messages and Sent.To name it by.
type Workspace = herdr.Workspace

// Prompt is one prompt to a project's session.
type Prompt struct {
	To       Workspace // the project's herdr workspace
	Checkout string    // the project's checkout, which the agent's cwd is in
	Tier     string    // the work item's tier, which picks the harness; "" for [tiers] default
	Text     string
	Date     string // the date of the note the closing line names
	Item     string // the work item the prompt is about, which records the pane; "" for none
	Ref      string // what the closing line names when Item is "", as owner/repo#N
}

// Deliver sends p, for which no herdr-prompt block holds the text, as Send sends a block's:
// to an available agent of p's harness, closed with the line asking it to update p's item, or
// p's ref when it has none (Closing). Sent.ID is p's item, or its ref when it has none.
func (s Sender) Deliver(ctx context.Context, p Prompt) (Sent, error) {
	p.Text = strings.TrimSpace(p.Text)
	if p.To.ID == "" || p.Text == "" {
		return Sent{}, fmt.Errorf("a prompt needs a workspace and a text: to %q, text %q", p.To.ID, p.Text)
	}
	if p.Item != "" && !note.ValidID(p.Item) {
		return Sent{}, fmt.Errorf("not an item id: %q", p.Item)
	}
	if p.To.Label == "" {
		p.To.Label = p.To.ID
	}
	sent, err := s.deliver(ctx, p)
	if p.Item == "" {
		sent.ID = p.Ref
	}
	return sent, err
}

// Closing is the line that ends every prompt sent, asking the agent to record its outcome on
// what, as Item(id) or `owner/repo#N`, in the note at path.
func Closing(what, path string) string {
	return ClosingLead + what + " in " + path + ". Record the outcome and refresh Next."
}

// ClosingLead starts every closing line (Closing), whatever it names: refresh ends a reply at
// the next one.
const ClosingLead = "When this is done, update the worklog with the worklog skill: "

// deliver sends p to the agent its harness picks, and records the agent's pane on p's item,
// unless the item's marker records a fresh dispatch (ws=), which it leaves as it is.
func (s Sender) deliver(ctx context.Context, p Prompt) (Sent, error) {
	if s.Engine == nil {
		return Sent{}, fmt.Errorf("no vault to record the send in")
	}
	if !note.IsDate(p.Date) {
		return Sent{}, fmt.Errorf("not a date: %q", p.Date)
	}
	if p.Checkout == "" {
		return Sent{}, fmt.Errorf("no checkout for the session in herdr workspace %q: set projects.root, or the project's path", p.To.Label)
	}
	name, h := s.Config.HarnessFor(p.Tier)
	if h.Kind == "" {
		return Sent{}, fmt.Errorf("[tiers] routes tier %q to no harness", p.Tier)
	}
	a, err := s.agent(ctx, p.To, p.Checkout, name, h)
	if err != nil {
		return Sent{}, err
	}
	path := filepath.Join(s.Engine.Repo, s.Engine.Dir, p.Date+".md")
	what := p.Ref
	if p.Item != "" {
		what = Item(p.Item)
	}
	if err := s.herdr().AgentPrompt(ctx, a.Pane, p.Text+"\n\n"+Closing(what, path)); err != nil {
		return Sent{}, err
	}
	sent := Sent{ID: p.Item, To: p.To.Label, Pane: a.Pane, Path: path}
	if p.Item == "" {
		return sent, nil
	}
	sent.Note, err = s.Engine.Mutate(ctx, p.Date, tool, func(n *note.Note, src []byte) ([]byte, string, error) {
		if n == nil {
			return nil, "", fmt.Errorf("no note %s", path)
		}
		if it := n.Item(p.Item); it != nil && it.Dispatch != nil && it.Dispatch.Workspace != "" {
			// A fresh dispatch's worker stays the item's agent: replacing its marker would
			// orphan its tab and checkout, which refresh closes and removes by the marker.
			return src, "send ^" + p.Item + " to herdr pane " + a.Pane, nil
		}
		out, err := note.SetDispatch(n, src, p.Item, note.Dispatch{Pane: a.Pane})
		return out, "send ^" + p.Item + " to herdr pane " + a.Pane, err
	})
	if err != nil {
		return sent, fmt.Errorf("prompted herdr pane %s, but it is not recorded on ^%s: %w", a.Pane, p.Item, err)
	}
	return sent, nil
}

// Item names work item id in the closing line, as Closing's what: `work item ^<id>`.
func Item(id string) string { return "work item ^" + id }

// PromptTag is the start of the closing line about work item id, up to the note's path: the part
// of Closing(Item(id), path) before the path. refresh finds an agent's reply to the prompt
// after it.
func PromptTag(id string) string { return ClosingLead + Item(id) + " in " }

// prompt returns the note, item id and the text of its herdr-prompt block. The note need not
// parse cleanly elsewhere; a parse error is reported only when the block is not found.
func prompt(src []byte, id string) (n *note.Note, it *note.Item, text string, err error) {
	n, perr := note.Parse(src)
	it = n.Item(id)
	if it == nil || it.Prompt == nil {
		err := fmt.Errorf("no herdr-prompt block with id=%q", id)
		if perr != nil {
			err = fmt.Errorf("%w; the note does not parse: %w", err, perr)
		}
		return nil, nil, "", err
	}
	text = Text(it.Prompt)
	if text == "" {
		return nil, nil, "", fmt.Errorf("block %q needs to= and a body", id)
	}
	return n, it, text, nil
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

// labelled is the one herdr workspace labelled label.
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

// project is the owner/repo whose session is in the herdr workspace label names, for item it
// of n: the project whose workspace override is label, else the project of the item's ref or of
// its section whose repo is named label and has no override.
func (s Sender) project(n *note.Note, it *note.Item, label string) (string, error) {
	var set []string
	for repo, p := range s.Config.Projects.Repos {
		if p.Workspace == label {
			set = append(set, repo)
		}
	}
	slices.Sort(set)
	switch len(set) {
	case 0:
	case 1:
		return set[0], nil
	default:
		return "", fmt.Errorf("projects %s each set workspace %q", strings.Join(set, ", "), label)
	}
	var repos []string
	if r, err := note.ParseRef(it.Ref, ""); err == nil {
		repos = append(repos, r.Repo)
	}
	for _, p := range n.Projects {
		if p.Remains == nil {
			continue
		}
		for _, b := range p.Remains.Buckets {
			if slices.Contains(b.Items, it) {
				repos = append(repos, p.Repo)
			}
		}
	}
	for _, repo := range repos {
		_, name, _ := strings.Cut(repo, "/")
		if name == label && s.Config.Projects.Project(repo).Workspace == "" {
			return repo, nil
		}
	}
	return "", fmt.Errorf("no project's session is in herdr workspace %q: set projects.\"<owner>/<repo>\".workspace = %q", label, label)
}

// agent is the agent a prompt for harness name, h, goes to in workspace ws: the first in
// `agent list` order of h's kind, idle or done, whose cwd is checkout or under it. A fresh
// dispatch's agent works in a checkout of its own, so it is never picked.
func (s Sender) agent(ctx context.Context, ws Workspace, checkout, name string, h config.Harness) (herdr.Agent, error) {
	agents, err := s.herdr().AgentList(ctx)
	if err != nil {
		return herdr.Agent{}, err
	}
	for _, a := range agents {
		if a.Workspace == ws.ID && a.Kind == h.Kind && slices.Contains(available, a.Status) && Within(checkout, a.Cwd) {
			return a, nil
		}
	}
	return herdr.Agent{}, fmt.Errorf("no available agent of harness %s in herdr workspace %q: none of kind %s in %s is idle or done",
		name, ws.Label, h.Kind, checkout)
}

// Within reports whether dir is root or under it, both absolute, by whole path components once
// cleaned: /a/b/c is within /a/b, and /a/bc is not.
func Within(root, dir string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(dir) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(dir))
	return err == nil && filepath.IsLocal(rel)
}

// sameDir reports whether a and b name one existing directory, through symlinks.
func sameDir(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && fa.IsDir() && os.SameFile(fa, fb)
}

func (s Sender) herdr() herdr.Client { return herdr.New(s.Herdr, s.Env) }
