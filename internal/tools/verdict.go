package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// CarryOverVerdictInput is carryover_verdict's input.
type CarryOverVerdictInput struct {
	Date    string      `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Row     int         `json:"row" jsonschema:"the carry-over row, 1-based in table order"`
	Verdict string      `json:"verdict" jsonschema:"keep (in scope today: it moves into its project's bucket), defer (tomorrow's open finds it again) or drop (closed or not relevant)"`
	Reason  string      `json:"reason,omitempty" jsonschema:"one line; needed to drop"`
	Item    *ItemFields `json:"item,omitempty" jsonschema:"keep's work item: id, repo and bucket, the rest as item_upsert takes it, its title the row's item when absent; an id already in the note only records the verdict"`
	State   string      `json:"state,omitempty" jsonschema:"keep's new item's state, TODO when absent"`
}

// CarryOverVerdictOutput is carryover_verdict's result.
type CarryOverVerdictOutput struct {
	Written
	Row  CarryOverView `json:"row" jsonschema:"the row as written"`
	Kept *ItemOutput   `json:"kept,omitempty" jsonschema:"the work item a keep moved it into"`
}

// carryOverVerdict records a verdict on one carry-over row; keep also writes the row's work
// item in its project's bucket, in the same change.
func (d Deps) carryOverVerdict(ctx context.Context, in CarryOverVerdictInput) (CarryOverVerdictOutput, error) {
	if in.Verdict != "keep" && (in.Item != nil || in.State != "") {
		return CarryOverVerdictOutput{}, fmt.Errorf("only keep moves a row into a bucket; %s takes no item or state", in.Verdict)
	}
	if in.Verdict == "keep" && in.Item == nil {
		return CarryOverVerdictOutput{}, errors.New("keep moves the row into its project's bucket: give item with its id, repo and bucket")
	}
	res, w, err := d.write(ctx, in.Date, "carryover_verdict", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		if n.CarryOver == nil || in.Row < 1 || in.Row > len(n.CarryOver.Rows) {
			rows := 0
			if n.CarryOver != nil {
				rows = len(n.CarryOver.Rows)
			}
			return nil, "", fmt.Errorf("row %d: the note has %d carry-over rows", in.Row, rows)
		}
		row := n.CarryOver.Rows[in.Row-1]
		cell, err := note.VerdictCell(in.Verdict, in.Reason)
		if err != nil {
			return nil, "", err
		}
		line, err := r.CarryOver(note.CarryOverUnit{Item: row.Item, State: row.State, Verdict: cell})
		if err != nil {
			return nil, "", err
		}
		out := note.Replace(src, row.Span, line)
		headline := "row " + strconv.Itoa(in.Row) + ": " + cell
		if in.Item == nil || n.Item(in.Item.ID) != nil {
			return out, headline, nil
		}
		if n, err = reparse(out); err != nil {
			return nil, "", err
		}
		f := *in.Item
		if f.Title == "" {
			f.Title = row.Item
		}
		s := note.Todo
		if in.State != "" {
			if s, err = note.ParseState(in.State); err != nil {
				return nil, "", err
			}
		}
		out, err = createItem(n, out, r, f, s.Icon(), "")
		return out, headline + ", as ^" + f.ID, err
	})
	if err != nil {
		return CarryOverVerdictOutput{}, err
	}
	row := res.Note.CarryOver.Rows[in.Row-1]
	o := CarryOverVerdictOutput{Written: w, Row: CarryOverView{Line: row.Line, Item: row.Item, State: row.State, Verdict: row.Verdict}}
	if in.Item != nil {
		k := itemOutput(res.Note, in.Item.ID)
		o.Kept = &k
	}
	return o, nil
}
