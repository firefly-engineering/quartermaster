// Package dispatch starts work on a ref or a work item (spec §9.2): task_dispatch's logic.
//
// Mode session prompts the project's herdr session through package send, the path of
// `worklog send`, and leaves the note alone. Mode fresh gives the ref, or a batch, a workspace
// of its own (ADR 0004), starts an agent there under herdr, then records the agent's pane and
// workspace on the work item and moves it to IN_PROGRESS, through the engine. Dispatched
// fresh again, as after an escalation, it reuses the workspace, and first closes its previous
// agent's tab under the guards refresh closes a finished one's with (CloseTab), or refuses
// while that agent runs.
//
// The order is the failure contract: workspace, then agent, then note. A herdr failure leaves
// the workspace in place and the item as it was; a note failure after the agent started
// reports the pane, so the user can record it by hand.
//
// Like every package but main, this one reads neither the environment nor the cwd: the
// Dispatcher carries the configuration, the executables' paths and their environment.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/engine"
	"github.com/firefly-engineering/quartermaster/internal/note"
	"github.com/firefly-engineering/quartermaster/internal/send"
)

// The modes.
const (
	Session = "session" // prompt the project's session; the default
	Fresh   = "fresh"   // a new workspace and a new agent
)

// tool names the mutation in the note's commit message.
const tool = "task_dispatch"

// summary is the one a fresh dispatch writes on the item it moves to IN_PROGRESS.
const summary = "dispatched to a fresh agent"

// Dispatcher dispatches refs and work items. The caller resolves every field.
type Dispatcher struct {
	Config         config.Config
	Engine         *engine.Engine // the note's only writer
	Herdr, JJ, Git string         // the executables' paths; "" when not found
	Env            []string       // their environment
}

// Result is a dispatch's outcome.
type Result struct {
	Ref       string // the ref; "" for a batch
	Mode      string
	Item      string // the work item; "" when a ref has none (session only)
	Path      string // the note's path
	To        string // the herdr workspace label prompted
	Pane      string // the herdr pane prompted
	Workspace string // fresh: the ref's or the batch's workspace
	Reused    bool   // fresh: the workspace existed already
	Closed    string // fresh: the herdr tab of the item's previous worker, closed first; "" for none
	Note      engine.Result
}

// Dispatch starts work on ref, whose work item, if any, is in the note of date, in mode
// (Session when ""). instruction, when not blank, is added to the prompt.
func (d Dispatcher) Dispatch(ctx context.Context, date string, ref note.Ref, mode, instruction string) (Result, error) {
	return d.dispatch(ctx, date, mode, instruction, func(n *note.Note) (job, error) {
		it, err := item(n, ref)
		return job{ref: &ref, repo: ref.Repo, item: it}, err
	})
}

// DispatchItem starts work on work item id, in the note of date, as Dispatch does. An item
// with a ref is dispatched as its ref. A batch, an item with none, is dispatched in its
// project's section, with its prompt; its fresh workspace, jj workspace, git branch and agent
// are named after id.
func (d Dispatcher) DispatchItem(ctx context.Context, date, id, mode, instruction string) (Result, error) {
	return d.dispatch(ctx, date, mode, instruction, func(n *note.Note) (job, error) {
		it := n.Item(id)
		if it == nil {
			return job{}, fmt.Errorf("no work item ^%s", id)
		}
		if s, ok := note.StateOf(it.Icon); ok && (s == note.Done || s == note.Dropped) {
			return job{}, fmt.Errorf("^%s is %s", it.ID, s)
		}
		if it.Ref != "" {
			ref, err := note.ParseRef(it.Ref, "")
			return job{ref: &ref, repo: ref.Repo, item: it}, err
		}
		switch {
		case it.Prompt == nil || send.Text(it.Prompt) == "":
			return job{}, fmt.Errorf("^%s has no ref and no prompt to start it with: item_upsert its prompt", id)
		case strings.HasPrefix(id, "-") || strings.HasPrefix(id, wsPrefix):
			return job{}, fmt.Errorf("^%s names a batch's workspace and branch, so it cannot start with - or %s", id, wsPrefix)
		}
		for _, p := range n.Projects {
			if it.Start >= p.Start && it.Start < p.End {
				return job{repo: p.Repo, item: it}, nil
			}
		}
		return job{}, fmt.Errorf("^%s is in no project section", id)
	})
}

// dispatch starts work on the job find picks from the note of date.
func (d Dispatcher) dispatch(ctx context.Context, date, mode, instruction string, find func(*note.Note) (job, error)) (Result, error) {
	if mode == "" {
		mode = Session
	}
	if mode != Session && mode != Fresh {
		return Result{}, fmt.Errorf("mode %q: want %s or %s", mode, Session, Fresh)
	}
	if !note.IsDate(date) {
		return Result{}, fmt.Errorf("not a date: %q, want YYYY-MM-DD", date)
	}
	path := filepath.Join(d.Engine.Repo, d.Engine.Dir, date+".md")
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && note.Empty(src) {
		return Result{}, fmt.Errorf("no note %s: day_open creates today's", path)
	}
	if err != nil {
		return Result{}, err
	}
	n, _ := note.Parse(src) // a write refuses a note that does not parse; finding the item need not
	j, err := find(n)
	if err != nil {
		return Result{}, err
	}
	res := Result{Mode: mode, Path: path}
	if j.ref != nil {
		res.Ref = j.ref.String()
	}
	if j.item != nil {
		res.Item = j.item.ID
	}
	if mode == Session {
		return d.session(ctx, date, j, instruction, res)
	}
	return d.fresh(ctx, date, j, instruction, res)
}

// job is what a dispatch starts work on: a ref and its work item, if any, or a batch, a work
// item with no ref.
type job struct {
	ref  *note.Ref  // nil for a batch
	repo string     // the project, owner/repo
	item *note.Item // nil for a ref with none
}

// name names a fresh dispatch's workspace directory, jj workspace and git branch: issue-<N>,
// or a batch's item id.
func (j job) name() string {
	if j.ref == nil {
		return j.item.ID
	}
	return wsPrefix + strconv.Itoa(j.ref.N)
}

// suffix ends the name of a fresh dispatch's agent, `<repo>-<suffix>`: the issue's number, or
// a batch's item id.
func (j job) suffix() string {
	if j.ref == nil {
		return j.item.ID
	}
	return strconv.Itoa(j.ref.N)
}

// prompt starts a fresh dispatch's work: `/implement <url>`, or a batch's own prompt, with
// instruction after it.
func (j job) prompt(instruction string) string {
	if j.ref != nil {
		return implement(*j.ref, instruction)
	}
	text := send.Text(j.item.Prompt)
	if instruction = strings.TrimSpace(instruction); instruction != "" {
		text += "\n\n" + instruction
	}
	return text
}

// item is ref's one open work item in n, or nil when it has none.
func item(n *note.Note, ref note.Ref) (*note.Item, error) {
	items := n.ItemsByRef(ref)
	switch len(items) {
	case 0:
		return nil, nil
	case 1:
	default:
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = "^" + it.ID
		}
		return nil, fmt.Errorf("%s names %d work items: %s", ref, len(items), strings.Join(ids, ", "))
	}
	it := items[0]
	if s, ok := note.StateOf(it.Icon); ok && (s == note.Done || s == note.Dropped) {
		return nil, fmt.Errorf("^%s, the work item of %s, is %s", it.ID, ref, s)
	}
	return it, nil
}

// session sends the item's prompt to its workspace, or `/implement <url>` to the project's.
func (d Dispatcher) session(ctx context.Context, date string, j job, instruction string, res Result) (Result, error) {
	ref, it := j.ref, j.item
	s := send.Sender{Herdr: d.Herdr, Env: d.Env}
	var sent send.Sent
	var err error
	if it != nil && it.Prompt != nil {
		sent, err = s.SendItem(ctx, filepath.Dir(res.Path), date, it.ID, instruction)
	} else {
		var to send.Workspace
		if to, err = d.herdrWorkspace(ctx, j.repo); err != nil {
			return Result{}, err
		}
		what := ref.String()
		if it != nil {
			what = send.Item(it.ID)
		}
		sent, err = s.Deliver(ctx, to, implement(*ref, instruction), what, res.Path)
	}
	if err != nil {
		return Result{}, err
	}
	res.To, res.Pane = sent.To, sent.Pane
	return res, nil
}

// fresh creates or reuses the job's workspace, starts an agent of the harness the item's tier
// routes to in it, and records both on the item.
func (d Dispatcher) fresh(ctx context.Context, date string, j job, instruction string, res Result) (Result, error) {
	it := j.item
	if it == nil {
		return Result{}, fmt.Errorf("no work item for %s in %s: a fresh dispatch records its agent on one, so item_upsert it first", j.ref, res.Path)
	}
	if from, _ := note.StateOf(it.Icon); from != note.InProgress {
		if err := note.Transition(from, note.InProgress, summary); err != nil {
			return Result{}, fmt.Errorf("^%s: %w", it.ID, err)
		}
	}
	checkout := d.Config.Projects.Project(j.repo).Path
	_, harness := d.Config.HarnessFor(it.Tier)
	switch {
	case checkout == "":
		return Result{}, fmt.Errorf("neither projects.root nor projects.%q.path is set: a fresh dispatch needs the checkout", j.repo)
	case harness.Kind == "":
		return Result{}, fmt.Errorf("[tiers] routes ^%s to no harness: a fresh dispatch needs the herdr agent kind to start", it.ID)
	case d.Config.Dispatch.WorkspaceRoot == "":
		return Result{}, errors.New("dispatch.workspace_root is not set")
	case d.Herdr == "":
		return Result{}, errors.New("herdr not found on PATH")
	}
	wsName := j.name()
	owner, repo, _ := strings.Cut(j.repo, "/")
	ws := filepath.Join(d.Config.Dispatch.WorkspaceRoot, owner, repo, wsName)
	// The marker must be writable before anything is made that it would record.
	if err := (note.Dispatch{Pane: "-", Workspace: ws}).Check(); err != nil {
		return Result{}, fmt.Errorf("dispatch.workspace_root: %w", err)
	}
	if fi, err := os.Stat(checkout); err != nil || !fi.IsDir() {
		return Result{}, fmt.Errorf("no checkout of %s at %s: clone it there, or set projects.%q.path", j.repo, checkout, j.repo)
	}
	// Finding the herdr workspace only reads, so it goes before anything is made.
	herdrWS, err := d.herdrWorkspace(ctx, j.repo)
	if err != nil {
		return Result{}, err
	}
	// A dispatch again starts its worker where the last one stopped: that one's tab goes first,
	// so two agents never write in one checkout, and its name is free again.
	if res.Closed, err = d.closePrevious(ctx, it); err != nil {
		return Result{}, err
	}
	// Naming the agent only reads, after the close that frees the name. Another agent may take
	// the name before it starts: herdr then refuses it, as any other herdr failure.
	taken, err := d.agentNames(ctx)
	if err != nil {
		return Result{}, err
	}
	agentName := AgentName(repo, j.suffix(), taken)
	if res.Reused, err = d.workspace(ctx, checkout, ws, wsName); err != nil {
		return Result{}, err
	}
	res.Workspace = ws

	prompt := j.prompt(instruction) + "\n\n" + send.Closing(send.Item(it.ID), res.Path)
	pane, err := d.agent(ctx, harness, herdrWS.ID, ws, agentName, prompt)
	if err != nil {
		left := "workspace " + ws + " is left in place"
		if pane != "" {
			left += ", with its herdr tab, pane " + pane
		}
		return Result{}, fmt.Errorf("%w; %s, and ^%s is not moved", err, left, it.ID)
	}
	res.To, res.Pane = herdrWS.Label, pane

	res.Note, err = d.Engine.Mutate(ctx, date, tool, func(n *note.Note, src []byte) ([]byte, string, error) {
		if n == nil {
			return nil, "", fmt.Errorf("no note %s", res.Path)
		}
		return record(n, src, it.ID, note.Dispatch{Pane: pane, Workspace: ws})
	})
	if err != nil {
		return Result{}, fmt.Errorf("the agent started in herdr pane %s, workspace %s, but it is not recorded on ^%s: %w",
			pane, ws, it.ID, err)
	}
	return res, nil
}

// closePrevious closes the herdr tab of the worker a fresh dispatch of it started before, under
// CloseTab's guards, and returns the tab it closed. A marker with no ws= records a session
// send's pane, never closed. It refuses while that worker is working or blocked, and while its
// tab stays for any other reason.
func (d Dispatcher) closePrevious(ctx context.Context, it *note.Item) (string, error) {
	prev := it.Dispatch
	if prev == nil || prev.Pane == "" || prev.Workspace == "" {
		return "", nil
	}
	h := d.herdr()
	tab, err := CloseTab(ctx, h, h.AgentList, prev.Pane, prev.Workspace)
	var kept *Kept
	switch {
	case errors.As(err, &kept) && kept.Running():
		return "", fmt.Errorf("^%s: its worker is still running: %s in pane %s is %s; dispatch it again once it is idle or done",
			it.ID, kept.Name, kept.Pane, kept.Status)
	case err != nil:
		return "", fmt.Errorf("^%s: not dispatched again: %w", it.ID, err)
	}
	return tab, nil
}

// record writes d on work item id and moves it to IN_PROGRESS, unless it is there already.
func record(n *note.Note, src []byte, id string, d note.Dispatch) ([]byte, string, error) {
	out, err := note.SetDispatch(n, src, id, d)
	if err != nil {
		return nil, "", err
	}
	if n, err = note.Parse(out); err != nil {
		return nil, "", fmt.Errorf("the marker would leave a note that does not parse: %w", err)
	}
	if s, _ := note.StateOf(n.Item(id).Icon); s != note.InProgress {
		if out, err = note.TransitionItem(n, out, id, note.InProgress, summary); err != nil {
			return nil, "", err
		}
	}
	return out, "dispatch ^" + id + " to herdr pane " + d.Pane, nil
}

// implement is the prompt that starts work on ref, with instruction after it.
func implement(ref note.Ref, instruction string) string {
	text := "/implement " + ref.URL()
	if instruction = strings.TrimSpace(instruction); instruction != "" {
		text += "\n\n" + instruction
	}
	return text
}
