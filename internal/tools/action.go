package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// ActionUpsertInput is action_upsert's input. A place is repo with bucket (Your actions when
// neither bucket nor part is given) or part, or process.
type ActionUpsertInput struct {
	Date    string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	ID      string `json:"id" jsonschema:"the manual action's id: lowercase letters, digits and -, unique in the note"`
	Text    string `json:"text,omitempty" jsonschema:"what only the user can do, one line; needed to create, unchanged when absent"`
	Repo    string `json:"repo,omitempty" jsonschema:"the project of its bucket or Part, owner/repo; with neither bucket nor part, its Your actions bucket"`
	Bucket  string `json:"bucket,omitempty" jsonschema:"the bucket it goes in, as: Your actions; in its project, or in repo"`
	Part    int    `json:"part,omitempty" jsonschema:"the Part N it goes in; in its project, or in repo"`
	Process bool   `json:"process,omitempty" jsonschema:"it goes in Process"`
}

// ActionUpsertOutput is action_upsert's result.
type ActionUpsertOutput struct {
	Written
	Created bool         `json:"created" jsonschema:"the manual action is new"`
	Where   string       `json:"where" jsonschema:"where it is: <repo>, <bucket>; <repo>, Part N; or Process"`
	Action  ManualAction `json:"action"`
}

// place is where a manual action is: a bucket or Part of project repo, or Process.
type place struct {
	repo, bucket string
	part         int
	process      bool
}

func (pl place) String() string {
	switch {
	case pl.process:
		return note.ProcessSection
	case pl.part != 0:
		return partName(pl.repo, pl.part)
	}
	return pl.repo + ", " + pl.bucket
}

// placeOf is where the unit at offset off of n is; ok is false outside a bucket, Part or Process.
func placeOf(n *note.Note, off int) (pl place, ok bool) {
	if n.Process != nil && off >= n.Process.Start && off < n.Process.End {
		return place{process: true}, true
	}
	p := projectAt(n, off)
	if p == nil {
		return place{}, false
	}
	if b := bucketAt(p, off); b != nil {
		return place{repo: p.Repo, bucket: b.Name}, true
	}
	for _, pt := range p.Parts {
		if off >= pt.Start && off < pt.End {
			return place{repo: p.Repo, part: pt.N}, true
		}
	}
	return place{}, false
}

// actionPlace is the place in, defaulting to what is, where the action is now; ok is false
// when in names none.
func actionPlace(in ActionUpsertInput, is place) (pl place, ok bool, err error) {
	if in.Process {
		if in.Repo != "" || in.Bucket != "" || in.Part != 0 {
			return place{}, false, errors.New("a manual action in Process has no repo, bucket or part")
		}
		return place{process: true}, true, nil
	}
	if in.Repo == "" && in.Bucket == "" && in.Part == 0 {
		return place{}, false, nil
	}
	if in.Bucket != "" && in.Part != 0 {
		return place{}, false, errors.New("a manual action goes in a bucket or a Part, not both")
	}
	pl = place{repo: in.Repo, bucket: in.Bucket, part: in.Part}
	if pl.repo == "" {
		if is.repo == "" {
			return place{}, false, errors.New("a bucket or part needs its repo")
		}
		pl.repo = is.repo
	}
	if pl.bucket == "" && pl.part == 0 {
		pl.bucket = note.ActionsBucket
	}
	return pl, true, nil
}

// insert writes line at the end of pl in n's source src.
func (pl place) insert(n *note.Note, src []byte, r note.Renderer, line []byte) ([]byte, error) {
	switch {
	case pl.process:
		return appendToProcess(n, src, r, line), nil
	case pl.part != 0:
		p, err := needProject(n, pl.repo)
		if err != nil {
			return nil, err
		}
		pt := partOf(p, pl.part)
		if pt == nil {
			return nil, fmt.Errorf("%w: %s has no Part %d; part_write writes it first", ErrNoPart, pl.repo, pl.part)
		}
		return appendLine(src, pt.End, pt.Lists, line), nil
	}
	src, _, b, err := ensureBucket(n, src, r, pl.repo, pl.bucket)
	if err != nil {
		return nil, err
	}
	return appendToBucket(src, b, line, false), nil
}

// actionUpsert creates a manual action, or rewords or moves one. It never ticks or unticks one:
// item_transition ticks it, and nothing unticks it (spec §4.4).
func (d Deps) actionUpsert(ctx context.Context, in ActionUpsertInput) (ActionUpsertOutput, error) {
	created := false
	res, w, err := d.write(ctx, in.Date, "action_upsert", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		a := n.Action(in.ID)
		created = a == nil
		if created {
			return createAction(n, src, r, in)
		}
		is, ok := placeOf(n, a.Start)
		if !ok {
			return nil, "", fmt.Errorf("^%s is outside a bucket, Part or Process: action_upsert cannot place it", in.ID)
		}
		to, move, err := actionPlace(in, is)
		if err != nil {
			return nil, "", err
		}
		text := in.Text
		if text == "" {
			text = a.Text
		}
		if !move || to == is {
			if in.Text == "" {
				return nil, "", fmt.Errorf("^%s: nothing to change: give its text or a new place", in.ID)
			}
			out, err := note.RewordAction(src, *a, text)
			return out, "reword ^" + in.ID, err
		}
		if a.Head.End != a.End {
			return nil, "", fmt.Errorf("^%s has nested lines, which a move would lose", in.ID)
		}
		line, err := r.ActionLine(note.ActionUnit{ID: in.ID, Text: text, Checked: a.Checked})
		if err != nil {
			return nil, "", err
		}
		out, err := removeLine(n, src, a.Span)
		if err != nil {
			return nil, "", err
		}
		if n, err = reparse(out); err != nil {
			return nil, "", err
		}
		out, err = to.insert(n, out, r, line)
		return out, "move ^" + in.ID + " to " + to.String(), err
	})
	if err != nil {
		return ActionUpsertOutput{}, err
	}
	a := res.Note.Action(in.ID)
	if a == nil {
		return ActionUpsertOutput{}, errors.New("the manual action is gone from the note it was written to")
	}
	where, _ := placeOf(res.Note, a.Start)
	return ActionUpsertOutput{
		Written: w, Created: created, Where: where.String(),
		Action: ManualAction{Line: a.Line, ID: a.ID, Text: a.Text, Checked: a.Checked},
	}, nil
}

// createAction writes a new, open manual action at the end of its place.
func createAction(n *note.Note, src []byte, r note.Renderer, in ActionUpsertInput) ([]byte, string, error) {
	if !note.ValidID(in.ID) {
		return nil, "", fmt.Errorf("invalid manual action id ^%s: lowercase letters, digits and - only", in.ID)
	}
	if n.Item(in.ID) != nil || slices.ContainsFunc(n.Anchors, func(a note.Anchor) bool { return a.ID == in.ID }) {
		return nil, "", fmt.Errorf("^%s already names a work item or anchor in the note", in.ID)
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, "", fmt.Errorf("a new manual action ^%s needs its text", in.ID)
	}
	to, ok, err := actionPlace(in, place{})
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", fmt.Errorf("a new manual action ^%s needs its place: repo, with bucket or part, or process", in.ID)
	}
	line, err := r.ActionLine(note.ActionUnit{ID: in.ID, Text: in.Text})
	if err != nil {
		return nil, "", err
	}
	out, err := to.insert(n, src, r, line)
	return out, "add ^" + in.ID + " to " + to.String(), err
}
