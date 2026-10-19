package tools

import (
	"context"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/dispatch"
	"github.com/firefly-engineering/quartermaster/internal/note"
)

// TaskDispatchInput is task_dispatch's input.
type TaskDispatchInput struct {
	Date        string `json:"date,omitempty" jsonschema:"the date of the note holding the ref's work item, YYYY-MM-DD; today (UTC) when absent"`
	Ref         string `json:"ref" jsonschema:"the issue or PR to start work on, owner/repo#N"`
	Mode        string `json:"mode,omitempty" jsonschema:"session (the default) prompts the project's herdr session; fresh starts a new agent in the ref's own workspace"`
	Instruction string `json:"instruction,omitempty" jsonschema:"added to the prompt, after the item's prompt or /implement <url>"`
}

// TaskDispatchOutput is task_dispatch's result.
type TaskDispatchOutput struct {
	Written
	Ref       string `json:"ref" jsonschema:"the ref, owner/repo#N"`
	Mode      string `json:"mode" jsonschema:"session or fresh"`
	Item      string `json:"item,omitempty" jsonschema:"the ref's work item; absent when the note has none"`
	To        string `json:"to" jsonschema:"the herdr workspace label prompted"`
	Pane      string `json:"pane" jsonschema:"the herdr pane prompted: fresh records it on the item"`
	Workspace string `json:"workspace,omitempty" jsonschema:"fresh: the ref's workspace, <workspace_root>/<owner>/<repo>/issue-<N>"`
	Reused    bool   `json:"reused,omitempty" jsonschema:"fresh: the workspace existed and was reused"`
	Closed    string `json:"closed,omitempty" jsonschema:"fresh: the herdr tab of the item's previous worker, closed before this one started"`
}

// taskDispatch starts work on a ref (spec §9.2) through package dispatch.
func (d Deps) taskDispatch(ctx context.Context, in TaskDispatchInput) (TaskDispatchOutput, error) {
	ref, err := note.ParseRef(in.Ref, "")
	if err != nil {
		return TaskDispatchOutput{}, err
	}
	if _, err := d.vault(); err != nil {
		return TaskDispatchOutput{}, err
	}
	date := in.Date
	if date == "" {
		date = d.today().Format(time.DateOnly)
	}
	res, err := dispatch.Dispatcher{
		Config: d.Config, Engine: d.engine(), Herdr: d.Herdr, JJ: d.JJ, Git: d.Git, Env: d.Env,
	}.Dispatch(ctx, date, ref, in.Mode, in.Instruction)
	if err != nil {
		return TaskDispatchOutput{}, err
	}
	n := res.Note
	return TaskDispatchOutput{
		Written: Written{Path: res.Path, Changed: n.Changed, Committed: n.Committed, Skipped: n.Skipped},
		Ref:     res.Ref, Mode: res.Mode, Item: res.Item, To: res.To, Pane: res.Pane,
		Workspace: res.Workspace, Reused: res.Reused, Closed: res.Closed,
	}, nil
}
