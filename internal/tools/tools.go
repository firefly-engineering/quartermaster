// Package tools holds the note's operations (docs/spec.md §6), each defined once and served
// two ways: as an MCP tool by `worklog serve`, and as a verb of `worklog note`.
//
// The pattern: a tool is one function from a typed input to a typed output, wrapped in a
// [Def] that names it as a tool and as a verb. The MCP server derives the JSON schemas of the
// input and output from those types, so their fields carry `json` and `jsonschema` tags; the
// CLI decodes the same input from one JSON argument and prints the same output as JSON. To add
// a tool, write its input and output types and its function next to the ones in day.go, and
// list its Def in [All]. A mutating tool does its edit inside [engine.Engine.Mutate], from
// Deps.engine; nothing else writes a note.
//
// Like every package but main, this one reads neither the environment nor the cwd: [Deps]
// carries the configuration, the clock, and the paths of jj, git, gh and herdr.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/firefly-engineering/quartermaster/internal/config"
	"github.com/firefly-engineering/quartermaster/internal/engine"
)

// Deps are what the tools need from outside, all resolved by main.
type Deps struct {
	Config config.Config
	Now    func() time.Time // the clock; tools read it in UTC
	JJ     string           // jj's path, "" when not found
	Git    string           // git's path, "" when not found
	GH     string           // gh's path, "" when not found
	Herdr  string           // herdr's path, "" when not found
	Env    []string         // the environment jj, git, gh and herdr run with
}

// Tool is one operation, servable over MCP and runnable from the CLI.
type Tool interface {
	// Names are the MCP tool name, as `day_open`, and the `worklog note` verb, as `open`.
	Names() (tool, verb string)
	// AddTo registers the tool on an MCP server.
	AddTo(s *mcp.Server)
	// Call runs the tool on a JSON object input, as the CLI does, and returns its output as
	// indented JSON. An empty input is `{}`.
	Call(ctx context.Context, input []byte) ([]byte, error)
}

// Def defines a tool from its function. In and Out are structs: their JSON schemas are the
// tool's input and output schemas.
type Def[In, Out any] struct {
	Name, Verb, Description string
	ReadOnly                bool // the tool never writes
	Run                     func(ctx context.Context, in In) (Out, error)
}

// Names implements Tool.
func (d Def[In, Out]) Names() (tool, verb string) { return d.Name, d.Verb }

// AddTo implements Tool.
func (d Def[In, Out]) AddTo(s *mcp.Server) {
	t := &mcp.Tool{Name: d.Name, Description: d.Description}
	if d.ReadOnly {
		t.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
	}
	mcp.AddTool(s, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := d.Run(ctx, in)
		return nil, out, err
	})
}

// Call implements Tool. Unknown input fields are refused, as the MCP schema refuses them.
func (d Def[In, Out]) Call(ctx context.Context, input []byte) ([]byte, error) {
	var in In
	if len(bytes.TrimSpace(input)) == 0 {
		input = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("%s: input: %w", d.Name, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s: input: one JSON object, not more", d.Name)
	}
	out, err := d.Run(ctx, in)
	if err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// All returns every tool, in spec §6's order.
func All(d Deps) []Tool {
	return []Tool{
		Def[DayOpenInput, DayOpenOutput]{
			Name: "day_open", Verb: "open",
			Description: "Create today's note from the template, with every open item of the previous note " +
				"(the latest dated note before today) as a carry-over row. A note prepared ahead is kept: it gains " +
				"only the rows it does not carry yet, and its status line reads open. One that does not parse is refused.",
			Run: d.dayOpen,
		},
		Def[DayReadInput, DayReadOutput]{
			Name: "day_read", Verb: "read", ReadOnly: true,
			Description: "Return a day's note as structured data: status, objectives, Next, items by project " +
				"and bucket with their states, carry-over, Process and manual actions, plus any parse errors.",
			Run: d.dayRead,
		},
		Def[ObjectiveSetInput, ObjectiveSetOutput]{
			Name: "objective_set", Verb: "objective",
			Description: "Add an objective (no row), or change one by its 1-based row: refine its objective or " +
				"done-condition (stamped `(refined HH:MM UTC: <reason>)`, reason required), set its progress, or " +
				"settle it as DONE or DROPPED (progress says why).",
			Run: d.objectiveSet,
		},
		Def[ItemUpsertInput, ItemUpsertOutput]{
			Name: "item_upsert", Verb: "item",
			Description: "Create a work item in a project's bucket, or rewrite one: title, ref (owner/repo#N, " +
				"rendered as a full link), cost, route, body, and a prompt for its ▶ button. A new project, What " +
				"remains or bucket is created as needed. An update rewrites the item from the input, keeping its " +
				"state and summary; a repo or bucket moves it. It warns, and writes all the same, when the item " +
				"is TODO with a /… route and no prompt, so no ▶ button.",
			Run: d.itemUpsert,
		},
		Def[ItemTransitionInput, ItemTransitionOutput]{
			Name: "item_transition", Verb: "transition",
			Description: "Move a work item through the state model (spec §4.3) with a summary: the PR, commit, " +
				"evidence or reason. Refuses an illegal move. A manual action can only be ticked, to DONE. With " +
				"part N and a move to DONE or DROPPED, the item leaves its bucket and Next and becomes a plain line " +
				"in its project's Part N, with its summary and without its id, button or prompt.",
			Run: d.itemTransition,
		},
		Def[PartWriteInput, PartWriteOutput]{
			Name: "part_write", Verb: "part",
			Description: "Create or rewrite one Part of a project whole: its number, kind (Human session, Agent " +
				"batch or Land), title, icon (🔄 running, 🔍 in review, ✅ done; written, never derived) and body. " +
				"The body is markdown: lines, a table, anchors (a paragraph ending ^<id>); no heading or work item.",
			Run: d.partWrite,
		},
		Def[ActionUpsertInput, ActionUpsertOutput]{
			Name: "action_upsert", Verb: "action",
			Description: "Create, reword or move a manual action: a checkbox with an id that only the user can " +
				"do, in a project's bucket (Your actions by default) or Part, or in Process. It keeps the box as it " +
				"is: item_transition ticks it, and nothing unticks it.",
			Run: d.actionUpsert,
		},
		Def[ProcessSetInput, ProcessSetOutput]{
			Name: "process_set", Verb: "process",
			Description: "Add a Process entry, or reword or settle one by its 1-based place. A settled entry " +
				"leaves Process, so the next day_open does not carry it over.",
			Run: d.processSet,
		},
		Def[NextSetInput, NextSetOutput]{
			Name: "next_set", Verb: "next",
			Description: "Replace Next with at most five actions, each named by its work item, manual action " +
				"or anchor id. The server renders each line: action, cost, details link and ▶ button. It warns, " +
				"and writes all the same, for each work item that is TODO with a /… route and no prompt, so no ▶ button.",
			Run: d.nextSet,
		},
		Def[StatusSetInput, StatusSetOutput]{
			Name: "status_set", Verb: "status",
			Description: "Rewrite the status line: `**HH:MM UTC, <label>:** <text>`, at the clock's time or at.",
			Run:         d.statusSet,
		},
		Def[CarryOverVerdictInput, CarryOverVerdictOutput]{
			Name: "carryover_verdict", Verb: "verdict",
			Description: "Record keep, defer or drop on a carry-over row, by its 1-based row. Keep also writes " +
				"the row's work item into its project's bucket; drop needs its reason.",
			Run: d.carryOverVerdict,
		},
		Def[RefreshInput, RefreshOutput]{
			Name: "refresh", Verb: "refresh",
			Description: "Reconcile the note with GitHub and herdr, applying only what the evidence proves " +
				"(spec §11): a merged PR or an issue closed as completed moves its item to DONE with the PR or " +
				"commit; an issue closed as not planned or a duplicate, to DROPPED; a dispatched IN_PROGRESS " +
				"item whose pane is gone or whose agent exited, to WAITING (\"worker exited\"), never DONE; a " +
				"reply with links to an item sent to another repo marks it acked. On a DONE or DROPPED item's fresh " +
				"dispatch, it closes the finished worker's tab, then, on a later refresh, removes its checkout, never " +
				"forced. Also ticks paired checkboxes " +
				"and derives icons. Returns what it changed and every item it checked and left, with why.",
			Run: d.refresh,
		},
		Def[PromptSendInput, PromptSendOutput]{
			Name: "prompt_send", Verb: "send",
			Description: "Send a work item's prompt block to the one agent in the herdr workspace its to= names, " +
				"as `worklog send` does, with the closing line asking it to update the item. Refuses a " +
				"blocked agent. Does not write the note.",
			Run: d.promptSend,
		},
		Def[TaskDispatchInput, TaskDispatchOutput]{
			Name: "task_dispatch", Verb: "dispatch",
			Description: "Start work on a ref, owner/repo#N, or a work item by id: a batch, which has no ref, or an " +
				"item dispatched as its ref. Mode session (the default) sends the work item's prompt, or /implement <url>, " +
				"to the project's herdr session, and leaves the note alone. Mode fresh creates or reuses the ref's " +
				"workspace, or the batch's, named after its item id (jj workspace, else git worktree, under " +
				"dispatch.workspace_root), starts the harness [tiers] maps the item's tier to (default when it has none) " +
				"in a new herdr tab there and prompts it, with /implement <url> or the batch's prompt, then records the " +
				"pane and workspace on the work item and moves it to IN_PROGRESS. Dispatched fresh again, it first " +
				"closes its previous worker's tab, and refuses while that worker is working or blocked.",
			Run: d.taskDispatch,
		},
	}
}

// Register adds every tool to an MCP server.
func Register(s *mcp.Server, ts []Tool) {
	for _, t := range ts {
		t.AddTo(s)
	}
}

// ErrUnknownVerb is returned by RunVerb for a verb no tool has.
var ErrUnknownVerb = errors.New("unknown verb")

// RunVerb runs the tool whose verb is verb on input, writing its JSON output to w.
func RunVerb(ctx context.Context, ts []Tool, verb string, input []byte, w io.Writer) error {
	for _, t := range ts {
		if _, v := t.Names(); v != verb {
			continue
		}
		out, err := t.Call(ctx, input)
		if err != nil {
			return err
		}
		_, err = w.Write(out)
		return err
	}
	return fmt.Errorf("%w %q", ErrUnknownVerb, verb)
}

// Verbs lists the tools' verbs, in order.
func Verbs(ts []Tool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		_, out[i] = t.Names()
	}
	return out
}

// vault is the Obsidian vault's directory.
func (d Deps) vault() (string, error) {
	if d.Config.Vault.Repo == "" {
		return "", errors.New("vault.repo is not set: set it in worklog.toml or pass --vault")
	}
	return filepath.Join(d.Config.Vault.Repo, d.Config.Vault.Dir), nil
}

// engine is the note's only writer, for this configuration.
func (d Deps) engine() *engine.Engine {
	return &engine.Engine{Repo: d.Config.Vault.Repo, Dir: d.Config.Vault.Dir, JJ: d.JJ, Env: d.Env}
}

// today is the clock's date, in UTC.
func (d Deps) today() time.Time { return d.Now().UTC() }
