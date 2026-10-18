package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// ObjectiveSetInput is objective_set's input. Without a row it adds an objective; with one it
// refines the row (a new objective or done-condition, stamped with the time and reason), sets
// its progress, or settles it (DONE, or DROPPED with the reason as progress).
type ObjectiveSetInput struct {
	Date      string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Row       int    `json:"row,omitempty" jsonschema:"the objective to change, 1-based in table order; absent adds one"`
	Objective string `json:"objective,omitempty" jsonschema:"the goal and its project, as: Ship the login page (acme/web); needed to add"`
	DoneWhen  string `json:"done_when,omitempty" jsonschema:"its checkable done-condition; needed to add"`
	State     string `json:"state,omitempty" jsonschema:"its progress icon's state: TODO, IN_PROGRESS, IN_REVIEW, WAITING, DONE or DROPPED; TODO for a new one, unchanged when absent"`
	Progress  string `json:"progress,omitempty" jsonschema:"the progress after the icon, as: 2/3 decisions or missed: no time; unchanged when absent"`
	Reason    string `json:"reason,omitempty" jsonschema:"why the objective or done-condition changed; needed to refine"`
	At        string `json:"at,omitempty" jsonschema:"HH:MM UTC of the refinement, when the user gave it; the clock's time when absent"`
}

// ObjectiveSetOutput is objective_set's result.
type ObjectiveSetOutput struct {
	Written
	Row       int           `json:"row" jsonschema:"the objective's row, 1-based"`
	Objective ObjectiveView `json:"objective" jsonschema:"the row as written"`
}

// objectiveSet adds, refines, progresses or settles one objective.
func (d Deps) objectiveSet(ctx context.Context, in ObjectiveSetInput) (ObjectiveSetOutput, error) {
	if in.Row < 0 {
		return ObjectiveSetOutput{}, fmt.Errorf("row %d: want 1 or more, or none to add", in.Row)
	}
	at, err := d.clock(in.At)
	if err != nil {
		return ObjectiveSetOutput{}, err
	}
	row := in.Row
	res, w, err := d.write(ctx, in.Date, "objective_set", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		if n.Objectives == nil || n.Objectives.Table == nil {
			return nil, "", errors.New("the note has no objectives table")
		}
		rows := n.Objectives.Rows
		if in.Row == 0 {
			row = len(rows) + 1
			return addObjective(in, src, n.Objectives.Table.End, r)
		}
		if in.Row > len(rows) {
			return nil, "", fmt.Errorf("row %d: the note has %d objectives", in.Row, len(rows))
		}
		return setObjective(in, rows[in.Row-1], at, src, r)
	})
	if err != nil {
		return ObjectiveSetOutput{}, err
	}
	o := res.Note.Objectives.Rows[row-1]
	return ObjectiveSetOutput{Written: w, Row: row, Objective: ObjectiveView{
		Line: o.Line, Objective: o.Objective, DoneWhen: o.DoneWhen, Progress: o.Progress, State: stateName(o.Icon),
	}}, nil
}

// addObjective appends a row to the table ending at end.
func addObjective(in ObjectiveSetInput, src []byte, end int, r note.Renderer) ([]byte, string, error) {
	if strings.TrimSpace(in.Objective) == "" || strings.TrimSpace(in.DoneWhen) == "" {
		return nil, "", errors.New("a new objective needs its objective and done_when")
	}
	if in.Reason != "" || in.At != "" {
		return nil, "", errors.New("a new objective has nothing to refine: leave out reason and at")
	}
	s := note.Todo
	if in.State != "" {
		var err error
		if s, err = note.ParseState(in.State); err != nil {
			return nil, "", err
		}
	}
	if err := needsProgress(s, in.Progress); err != nil {
		return nil, "", err
	}
	line, err := r.Objective(note.ObjectiveUnit{Objective: in.Objective, DoneWhen: in.DoneWhen, Icon: s.Icon(), Progress: in.Progress})
	if err != nil {
		return nil, "", err
	}
	return note.Insert(src, end, line), "add objective: " + in.Objective, nil
}

// setObjective rewrites row o from what in sets; the rest of the row stays as written.
func setObjective(in ObjectiveSetInput, o note.Objective, at string, src []byte, r note.Renderer) ([]byte, string, error) {
	u := note.ObjectiveUnit{
		Objective: o.Objective, DoneWhen: o.DoneWhen, Icon: o.Icon,
		Progress: strings.TrimSpace(strings.TrimPrefix(o.Progress, string(o.Icon))),
	}
	var what []string
	objective, doneWhen := in.Objective != "" && in.Objective != note.StripRefined(o.Objective), in.DoneWhen != "" && in.DoneWhen != o.DoneWhen
	if objective || doneWhen {
		if strings.TrimSpace(in.Reason) == "" {
			return nil, "", errors.New("refining an objective needs its reason")
		}
		u.Objective = note.StripRefined(o.Objective)
		if objective {
			u.Objective = in.Objective
		}
		if doneWhen {
			u.DoneWhen = in.DoneWhen
		}
		u.Refined, u.Reason = at, in.Reason
		what = append(what, "refined: "+in.Reason)
	} else if in.Reason != "" || in.At != "" {
		return nil, "", errors.New("a reason and at time a refinement of the objective or done_when, and neither changes")
	}
	if in.State != "" {
		s, err := note.ParseState(in.State)
		if err != nil {
			return nil, "", err
		}
		u.Icon = s.Icon()
		what = append(what, s.String())
	}
	if in.Progress != "" {
		u.Progress = in.Progress
		what = append(what, in.Progress)
	}
	if len(what) == 0 {
		return nil, "", errors.New("nothing to set: give objective, done_when, state or progress")
	}
	s, _ := note.StateOf(u.Icon)
	if err := needsProgress(s, u.Progress); err != nil {
		return nil, "", err
	}
	line, err := r.Objective(u)
	if err != nil {
		return nil, "", err
	}
	return note.Replace(src, o.Span, line), "objective " + strconv.Itoa(in.Row) + ": " + strings.Join(what, ", "), nil
}

// needsProgress refuses a WAITING or DROPPED objective whose progress does not say why.
func needsProgress(s note.State, progress string) error {
	if (s == note.Waiting || s == note.Dropped) && strings.TrimSpace(progress) == "" {
		return fmt.Errorf("a %s objective needs its progress to say why", s)
	}
	return nil
}
