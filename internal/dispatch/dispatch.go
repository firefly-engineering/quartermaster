// Package dispatch starts work on a ref (spec §9.2): task_dispatch's logic.
//
// Mode session prompts the project's herdr session through package send, the path of
// `worklog send`, and leaves the note alone. Mode fresh gives the ref a workspace of its
// own (ADR 0004), starts an agent there under herdr, then records the agent's pane and
// workspace on the ref's work item and moves it to IN_PROGRESS, through the engine.
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

// Dispatcher dispatches refs. The caller resolves every field.
type Dispatcher struct {
	Config         config.Config
	Engine         *engine.Engine // the note's only writer
	Herdr, JJ, Git string         // the executables' paths; "" when not found
	Env            []string       // their environment
}

// Result is a dispatch's outcome.
type Result struct {
	Ref, Mode string
	Item      string // the ref's work item; "" when it has none (session only)
	Path      string // the note's path
	To        string // the herdr workspace label prompted
	Pane      string // the herdr pane prompted
	Workspace string // fresh: the ref's workspace
	Reused    bool   // fresh: the workspace existed already
	Note      engine.Result
}

// Dispatch starts work on ref, whose work item, if any, is in the note of date, in mode
// (Session when ""). instruction, when not blank, is added to the prompt.
func (d Dispatcher) Dispatch(ctx context.Context, date string, ref note.Ref, mode, instruction string) (Result, error) {
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
	it, err := item(n, ref)
	if err != nil {
		return Result{}, err
	}
	res := Result{Ref: ref.String(), Mode: mode, Path: path}
	if it != nil {
		res.Item = it.ID
	}
	if mode == Session {
		return d.session(ctx, date, ref, it, instruction, res)
	}
	return d.fresh(ctx, date, ref, it, instruction, res)
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
func (d Dispatcher) session(ctx context.Context, date string, ref note.Ref, it *note.Item, instruction string, res Result) (Result, error) {
	s := send.Sender{Herdr: d.Herdr, Env: d.Env}
	var sent send.Sent
	var err error
	if it != nil && it.Prompt != nil {
		sent, err = s.SendItem(ctx, filepath.Dir(res.Path), date, it.ID, instruction)
	} else {
		var p config.Project
		if p, err = d.project(ref); err != nil {
			return Result{}, err
		}
		what := ref.String()
		if it != nil {
			what = send.Item(it.ID)
		}
		sent, err = s.Deliver(ctx, p.Workspace, implement(ref, instruction), what, res.Path)
	}
	if err != nil {
		return Result{}, err
	}
	res.To, res.Pane = sent.To, sent.Pane
	return res, nil
}

// fresh creates or reuses the ref's workspace, starts an agent in it, and records both on the
// item.
func (d Dispatcher) fresh(ctx context.Context, date string, ref note.Ref, it *note.Item, instruction string, res Result) (Result, error) {
	if it == nil {
		return Result{}, fmt.Errorf("no work item for %s in %s: a fresh dispatch records its agent on one, so item_upsert it first", ref, res.Path)
	}
	if from, _ := note.StateOf(it.Icon); from != note.InProgress {
		if err := note.Transition(from, note.InProgress, summary); err != nil {
			return Result{}, fmt.Errorf("^%s: %w", it.ID, err)
		}
	}
	p, err := d.project(ref)
	if err != nil {
		return Result{}, err
	}
	switch {
	case p.Path == "":
		return Result{}, fmt.Errorf("projects.%q.path is not set: a fresh dispatch needs the checkout", ref.Repo)
	case d.Config.Harness.Worker.Kind == "":
		return Result{}, errors.New("harness.worker.kind is not set: a fresh dispatch needs the herdr agent kind to start")
	case d.Config.Dispatch.WorkspaceRoot == "":
		return Result{}, errors.New("dispatch.workspace_root is not set")
	case d.Herdr == "":
		return Result{}, errors.New("herdr not found on PATH")
	}
	name := "issue-" + strconv.Itoa(ref.N)
	owner, repo, _ := strings.Cut(ref.Repo, "/")
	ws := filepath.Join(d.Config.Dispatch.WorkspaceRoot, owner, repo, name)
	// The marker must be writable before anything is made that it would record.
	if err := (note.Dispatch{Pane: "-", Workspace: ws}).Check(); err != nil {
		return Result{}, fmt.Errorf("dispatch.workspace_root: %w", err)
	}
	if res.Reused, err = d.workspace(ctx, p.Path, ws, name); err != nil {
		return Result{}, err
	}
	res.Workspace = ws

	prompt := implement(ref, instruction) + "\n\n" + send.Closing(send.Item(it.ID), res.Path)
	pane, err := d.agent(ctx, p.Workspace, ws, name, prompt)
	if err != nil {
		left := "workspace " + ws + " is left in place"
		if pane != "" {
			left += ", with its herdr tab, pane " + pane
		}
		return Result{}, fmt.Errorf("%w; %s, and ^%s is not moved", err, left, it.ID)
	}
	res.To, res.Pane = p.Workspace, pane

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

// project is ref's project in the configuration, with its herdr workspace label.
func (d Dispatcher) project(ref note.Ref) (config.Project, error) {
	p, ok := d.Config.Projects[ref.Repo]
	if !ok || p.Workspace == "" {
		return config.Project{}, fmt.Errorf("no project %s with a herdr workspace in the configuration: set projects.%q.workspace", ref.Repo, ref.Repo)
	}
	return p, nil
}

// implement is the prompt that starts work on ref, with instruction after it.
func implement(ref note.Ref, instruction string) string {
	text := "/implement " + ref.URL()
	if instruction = strings.TrimSpace(instruction); instruction != "" {
		text += "\n\n" + instruction
	}
	return text
}
