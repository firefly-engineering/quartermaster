package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/dispatch"
	"github.com/firefly-engineering/quartermaster/internal/note"
)

// TaskDispatchInput is task_dispatch's input.
type TaskDispatchInput struct {
	Date        string `json:"date,omitempty" jsonschema:"the date of the note holding the work item, YYYY-MM-DD; today (UTC) when absent"`
	Ref         string `json:"ref,omitempty" jsonschema:"the issue or PR to start work on, owner/repo#N; give it or item, not both"`
	Item        string `json:"item,omitempty" jsonschema:"the work item to start work on, by id: a batch, which has no ref, or an item with one, dispatched as its ref; give it or ref, not both"`
	Mode        string `json:"mode,omitempty" jsonschema:"session (the default) prompts the project's herdr session; fresh starts a new agent in the ref's or the batch's own workspace"`
	Instruction string `json:"instruction,omitempty" jsonschema:"added to the prompt, after the item's prompt or /implement <url>"`
}

// TaskDispatchOutput is task_dispatch's result.
type TaskDispatchOutput struct {
	Written
	Ref       string `json:"ref,omitempty" jsonschema:"the ref, owner/repo#N; absent for a batch"`
	Mode      string `json:"mode" jsonschema:"session or fresh"`
	Item      string `json:"item,omitempty" jsonschema:"the work item; absent when the note has none for the ref"`
	To        string `json:"to" jsonschema:"the herdr workspace label prompted"`
	Pane      string `json:"pane" jsonschema:"the herdr pane prompted, recorded on the work item when it has one"`
	Workspace string `json:"workspace,omitempty" jsonschema:"fresh: the workspace, <workspace_root>/<owner>/<repo>/issue-<N>, or <workspace_root>/<owner>/<repo>/<item-id> for a batch"`
	Reused    bool   `json:"reused,omitempty" jsonschema:"fresh: the workspace existed and was reused"`
	Closed    string `json:"closed,omitempty" jsonschema:"fresh: the herdr tab of the item's previous worker, closed before this one started"`
}

// taskDispatch starts work on a ref or a work item (spec §9.2) through package dispatch.
func (d Deps) taskDispatch(ctx context.Context, in TaskDispatchInput) (TaskDispatchOutput, error) {
	var ref note.Ref
	switch {
	case (in.Ref == "") == (in.Item == ""):
		return TaskDispatchOutput{}, errors.New("give ref or item, not both")
	case in.Item != "" && !note.ValidID(in.Item):
		return TaskDispatchOutput{}, fmt.Errorf("not an item id: %q", in.Item)
	case in.Ref != "":
		var err error
		if ref, err = note.ParseRef(in.Ref, ""); err != nil {
			return TaskDispatchOutput{}, err
		}
	}
	if _, err := d.vault(); err != nil {
		return TaskDispatchOutput{}, err
	}
	date := in.Date
	if date == "" {
		date = d.today().Format(time.DateOnly)
	}
	dd := dispatch.Dispatcher{
		Config: d.Config, Engine: d.engine(), Herdr: d.Herdr, JJ: d.JJ, Git: d.Git, Env: d.Env,
	}
	var res dispatch.Result
	var err error
	if in.Item != "" {
		res, err = dd.DispatchItem(ctx, date, in.Item, in.Mode, in.Instruction)
	} else {
		res, err = dd.Dispatch(ctx, date, ref, in.Mode, in.Instruction)
	}
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
