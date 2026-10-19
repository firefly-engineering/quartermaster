// Package send sends a prompt to a project's session (spec §9.1): the ▶ button handler,
// prompt_send, and task_dispatch's session mode all send through it.
//
// Any web page can open the button's obsidian:// URI, so the only inputs are a date and an
// item id, both validated. The prompt text comes from the user's own note, and reaches
// herdr as an argument, never through a shell. The work item's tier picks a harness through
// config, and the harness's kind picks the agent, or one is spawned when none is available
// (ADR 0011). Each send records the agent's pane on the item, through the engine.
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
var available = []string{herdr.Idle, herdr.Done}

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
// workspace its block names, spawned when there is none, and records the agent's pane on the
// item. A block's to=owner/repo names the project, whose workspace is opened if it has none;
// a bare label names the workspace, only looked up.
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
	p := Prompt{Tier: it.Tier, Text: text, Date: date, Item: id}
	// to=owner/repo names the project, whose workspace deliver resolves, creating it if need
	// be. A bare label names a workspace, only looked up.
	if p.Repo = it.Prompt.To; !note.ValidRepo(p.Repo) {
		if p.To, err = s.workspace(ctx, it.Prompt.To); err != nil {
			return Sent{}, err
		}
		if p.Repo, err = s.project(n, it, it.Prompt.To); err != nil {
			return Sent{}, err
		}
	}
	p.Checkout = s.Config.Projects.Project(p.Repo).Path
	return s.deliver(ctx, p)
}

// Workspace is one herdr workspace: its id, and the label messages and Sent.To name it by.
type Workspace = herdr.Workspace

// Prompt is one prompt to a project's session.
type Prompt struct {
	To       Workspace // the project's herdr workspace; resolved from Repo when zero (ProjectWorkspace)
	Repo     string    // the project, owner/repo, which names an agent spawned for it
	Checkout string    // the project's checkout, which the agent's cwd is in
	Tier     string    // the work item's tier, which picks the harness; "" for [tiers] default
	Text     string
	Date     string // the date of the note the closing line names
	Item     string // the work item the prompt is about, which records the pane; "" for none
	Ref      string // what the closing line names when Item is "", as owner/repo#N

	root string // the initial shell pane of To, when deliver just created it, which a spawn takes
}

// Deliver sends p, for which no herdr-prompt block holds the text, as Send sends a block's:
// to an available agent of p's harness, closed with the line asking it to update p's item, or
// p's ref when it has none (Closing). With no To, it goes to the project's herdr workspace,
// created if need be (ProjectWorkspace). Sent.ID is p's item, or its ref when it has none.
func (s Sender) Deliver(ctx context.Context, p Prompt) (Sent, error) {
	p.Text = strings.TrimSpace(p.Text)
	if p.Repo == "" || p.Text == "" {
		return Sent{}, fmt.Errorf("a prompt needs a project and a text: repo %q, text %q", p.Repo, p.Text)
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
		return Sent{}, fmt.Errorf("no checkout for %s's session: set projects.root, or projects.%q.path", p.Repo, p.Repo)
	}
	name, h := s.Config.HarnessFor(p.Tier)
	if h.Kind == "" {
		return Sent{}, fmt.Errorf("[tiers] routes tier %q to no harness", p.Tier)
	}
	if p.To.ID == "" {
		var err error
		if p.To, p.root, err = ProjectWorkspace(ctx, s.herdr(), s.Config.Projects, p.Repo); err != nil {
			return Sent{}, err
		}
	}
	a, err := s.agent(ctx, p, name, h)
	if err != nil {
		return Sent{}, err
	}
	path := filepath.Join(s.Engine.Repo, s.Engine.Dir, p.Date+".md")
	what := p.Ref
	if p.Item != "" {
		what = Item(p.Item)
	}
	if err := s.herdr().AgentPrompt(ctx, a.Pane, h.Prompt(p.Text)+"\n\n"+Closing(what, path)); err != nil {
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

// workspace is the one herdr workspace labelled label, among those herdr lists (labelled).
func (s Sender) workspace(ctx context.Context, label string) (Workspace, error) {
	spaces, err := s.herdr().WorkspaceList(ctx)
	if err != nil {
		return Workspace{}, err
	}
	ws, n := labelled(spaces, label)
	if n != 1 {
		return Workspace{}, fmt.Errorf("%d herdr workspaces labelled %q", n, label)
	}
	return ws, nil
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

// agent is the agent p goes to for harness name, h: the first in `agent list` order in p's
// workspace, of h's kind, idle or done, whose cwd is p's checkout or under it. A fresh
// dispatch's agent works in a checkout of its own, so it is never picked. With none available,
// busy ones of the kind included, it spawns one.
func (s Sender) agent(ctx context.Context, p Prompt, name string, h config.Harness) (herdr.Agent, error) {
	agents, err := s.herdr().AgentList(ctx)
	if err != nil {
		return herdr.Agent{}, err
	}
	for _, a := range agents {
		if a.Workspace == p.To.ID && a.Kind == h.Kind && slices.Contains(available, a.Status) && Within(p.Checkout, a.Cwd) {
			return a, nil
		}
	}
	return s.spawn(ctx, p, name, h, agents)
}

// spawn starts an agent of harness name, h, for p, and returns it: in the initial shell pane of
// p's workspace when deliver just created it, else in a new tab of the workspace at its
// checkout, without focus. It is named the first free `<repo>-<harness>`
// among live, all of herdr's agents (AgentName). The tool never closes it: a session keeps its
// warm context.
func (s Sender) spawn(ctx context.Context, p Prompt, name string, h config.Harness, live []herdr.Agent) (herdr.Agent, error) {
	taken := make([]string, len(live))
	for i, a := range live {
		taken[i] = a.Name
	}
	_, repo, _ := strings.Cut(p.Repo, "/")
	agent := AgentName(repo, name, taken)
	if p.root != "" {
		if err := s.herdr().AgentStart(ctx, agent, h.Kind, p.root, h.Args); err != nil {
			return herdr.Agent{}, fmt.Errorf("%w; herdr workspace %q, created for it, is left in place", err, p.To.Label)
		}
		return herdr.Agent{Name: agent, Kind: h.Kind, Workspace: p.To.ID, Pane: p.root, Cwd: p.Checkout}, nil
	}
	pane, err := Start(ctx, s.herdr(), h, p.To.ID, p.Checkout, agent)
	if err != nil {
		if pane != "" {
			err = fmt.Errorf("%w; its herdr tab, pane %s, is left in place", err, pane)
		}
		return herdr.Agent{}, err
	}
	return herdr.Agent{Name: agent, Kind: h.Kind, Workspace: p.To.ID, Pane: pane, Cwd: p.Checkout}, nil
}

// herdr 0.9.5 has no call that creates a pane running a command, so an agent starts in two,
// and is prompted in a third (spec §9.2), each checked against `herdr <cmd> --help`:
//
//	herdr tab create --workspace <id> --cwd <path> --label <text> --no-focus
//	herdr agent start <name> --kind <kind> --pane <id> [-- <agent-args>...]
//	herdr agent prompt <pane> <text>

// Start starts harness h's agent, named name, in a new tab of herdr workspace ws at cwd,
// labelled name, without taking focus. pane is the tab's pane once created, even when the
// agent does not start. A session's spawn and a fresh dispatch both start their agents here.
func Start(ctx context.Context, c herdr.Client, h config.Harness, ws, cwd, name string) (pane string, err error) {
	if pane, err = c.TabCreate(ctx, ws, cwd, name); err != nil {
		return "", err
	}
	return pane, c.AgentStart(ctx, name, h.Kind, pane, h.Args)
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
