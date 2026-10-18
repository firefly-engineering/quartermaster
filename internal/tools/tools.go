// Package tools holds the note's operations (docs/spec.md §6), each defined once and served
// two ways: as an MCP tool by `quartermaster serve`, and as a verb of `quartermaster note`.
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
	// Names are the MCP tool name, as `day_open`, and the `quartermaster note` verb, as `open`.
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
				"(the latest dated note before today) as a carry-over row. Refuses if a non-empty note exists.",
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
				"state and summary; a repo or bucket moves it.",
			Run: d.itemUpsert,
		},
		Def[ItemTransitionInput, ItemTransitionOutput]{
			Name: "item_transition", Verb: "transition",
			Description: "Move a work item through the state model (spec §4.3) with a summary: the PR, commit, " +
				"evidence or reason. Refuses an illegal move. A manual action can only be ticked, to DONE.",
			Run: d.itemTransition,
		},
		Def[NextSetInput, NextSetOutput]{
			Name: "next_set", Verb: "next",
			Description: "Replace Next with at most five actions, each named by its work item, manual action " +
				"or anchor id. The server renders each line: action, cost, details link and ▶ button.",
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
				"reply with links to an item sent to another repo marks it acked. Also ticks paired checkboxes " +
				"and derives icons. Returns what it changed and every item it checked and left, with why.",
			Run: d.refresh,
		},
		Def[TaskDispatchInput, TaskDispatchOutput]{
			Name: "task_dispatch", Verb: "dispatch",
			Description: "Start work on a ref, owner/repo#N. Mode session (the default) sends its work item's prompt, " +
				"or /implement <url>, to the project's herdr session, and leaves the note alone. Mode fresh creates or " +
				"reuses the ref's workspace (jj workspace, else git worktree, under dispatch.workspace_root), starts " +
				"harness.worker in a new herdr tab there and prompts it, then records the pane and workspace on the " +
				"ref's work item and moves it to IN_PROGRESS.",
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
		return "", errors.New("vault.repo is not set: set it in config.toml or pass --vault")
	}
	return filepath.Join(d.Config.Vault.Repo, d.Config.Vault.Dir), nil
}

// engine is the note's only writer, for this configuration.
func (d Deps) engine() *engine.Engine {
	return &engine.Engine{Repo: d.Config.Vault.Repo, Dir: d.Config.Vault.Dir, JJ: d.JJ, Env: d.Env}
}

// today is the clock's date, in UTC.
func (d Deps) today() time.Time { return d.Now().UTC() }
