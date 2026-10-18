package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// maxNext is how many actions *Next* holds at most (GLOSSARY.md, the skill's note layout).
const maxNext = 5

// NextSetInput is next_set's input.
type NextSetInput struct {
	Date  string      `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Items []NextInput `json:"items" jsonschema:"the actions in the order to take them, at most five; empty clears Next"`
}

// NextInput is one action of *Next*, named by the item its details link points at.
type NextInput struct {
	ID     string `json:"id" jsonschema:"a work item, manual action or anchor id"`
	Action string `json:"action,omitempty" jsonschema:"what to do, one line; the item's title or the action's text when absent, needed for an anchor"`
	Cost   string `json:"cost,omitempty" jsonschema:"what it costs, as: 20 min; the work item's cost when absent, none for a manual action or anchor"`
}

// NextSetOutput is next_set's result.
type NextSetOutput struct {
	Written
	Next     []NextView `json:"next" jsonschema:"Next as written"`
	Dropped  []NextView `json:"dropped" jsonschema:"the lines Next held that it no longer does"`
	Warnings []string   `json:"warnings,omitempty" jsonschema:"one line each: a work item in Next that is TODO with a /… route and no prompt, which has no ▶ button; the write went through"`
}

// nextSet replaces *Next* with one rendered line per id: its action, cost, details link and,
// for a work item with a prompt, its ▶ button. A line for a manual action is a checkbox, ticked
// when the action or its line in Next was: a box the user ticked stays ticked, whether its line
// is kept or dropped. Lines that link nothing are dropped, and reported.
func (d Deps) nextSet(ctx context.Context, in NextSetInput) (NextSetOutput, error) {
	if len(in.Items) > maxNext {
		return NextSetOutput{}, fmt.Errorf("next holds at most %d actions, not %d", maxNext, len(in.Items))
	}
	var dropped []NextView
	res, w, err := d.write(ctx, in.Date, "next_set", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		if n.Next == nil {
			return nil, "", errors.New("the note has no ## Next section")
		}
		// A Next line ticked in Obsidian ticks its manual action before the line may go, as
		// the next write must tick both (spec §4.4).
		if ticks := n.Reconcile(); len(ticks) > 0 {
			src = note.Tick(src, ticks)
			var err error
			if n, err = reparse(src); err != nil {
				return nil, "", err
			}
		}
		units := make([]note.NextUnit, len(in.Items))
		ids := make([]string, len(in.Items))
		for i, x := range in.Items {
			if slices.Contains(ids[:i], x.ID) {
				return nil, "", fmt.Errorf("^%s is in Next twice", x.ID)
			}
			ids[i] = x.ID
			var err error
			if units[i], err = nextUnit(n, x); err != nil {
				return nil, "", err
			}
		}
		lines, err := r.NextBody(units)
		if err != nil {
			return nil, "", err
		}
		dropped = []NextView{}
		for _, it := range n.Next.Items {
			if it.Ref == "" || !slices.Contains(ids, it.Ref) {
				dropped = append(dropped, nextView(src, it))
			}
		}
		body := note.Span{Start: lineEnd(src, n.Next.Start), End: n.Next.End}
		headline := "next: " + strings.Join(ids, ", ")
		if len(ids) == 0 {
			headline = "next: cleared"
		}
		return note.Replace(src, body, lines), headline, nil
	})
	if err != nil {
		return NextSetOutput{}, err
	}
	o := NextSetOutput{Written: w, Next: []NextView{}, Dropped: dropped}
	for _, it := range res.Note.Next.Items {
		o.Next = append(o.Next, nextView(res.Src, it))
		if item := res.Note.Item(it.Ref); item != nil && noButton(item) {
			o.Warnings = append(o.Warnings, noButtonWarning(item))
		}
	}
	return o, nil
}

// nextUnit is x's line: from the work item, manual action or anchor x.ID names. A work item
// gives its title and cost where x leaves them out; nothing is read back from Next's line.
func nextUnit(n *note.Note, x NextInput) (note.NextUnit, error) {
	u := note.NextUnit{Action: strings.TrimSpace(x.Action), Cost: x.Cost, Ref: x.ID}
	var was note.Box // the box of x.ID's line in Next now, NoBox for none
	for _, it := range n.Next.Items {
		if it.Ref == x.ID && it.Box > was {
			was = it.Box
		}
	}
	switch it, a := n.Item(x.ID), n.Action(x.ID); {
	case it != nil:
		if u.Action == "" {
			u.Action = note.Action(it.Title)
		}
		if u.Cost == "" {
			u.Cost = it.Cost
		}
		if it.Prompt != nil {
			u.Button = it.Button
			if u.Button == "" {
				u.Button = note.DefaultButton(it.Prompt.To)
			}
		}
		u.Box = was
	case a != nil:
		if u.Action == "" {
			u.Action = a.Text
		}
		u.Box = note.Unchecked
		if a.Checked || was == note.Checked {
			u.Box = note.Checked
		}
	case slices.ContainsFunc(n.Anchors, func(an note.Anchor) bool { return an.ID == x.ID }):
		if u.Action == "" {
			return note.NextUnit{}, fmt.Errorf("^%s is an anchor, with no title: give its action", x.ID)
		}
		u.Box = was
	default:
		return note.NextUnit{}, fmt.Errorf("^%s names no work item, manual action or anchor", x.ID)
	}
	if u.Box != note.NoBox {
		u.Button = "" // a manual action has no button: no session can start it
	}
	return u, nil
}

func nextView(src []byte, it note.NextItem) NextView {
	box := map[note.Box]string{note.Checked: "checked", note.Unchecked: "unchecked"}[it.Box]
	return NextView{Line: it.Line, Text: source(src, it.Span), Box: box, Ref: it.Ref}
}
