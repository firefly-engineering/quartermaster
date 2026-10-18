package tools

import (
	"context"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// StatusSetInput is status_set's input.
type StatusSetInput struct {
	Date  string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Text  string `json:"text" jsonschema:"one line: where the day stands"`
	Label string `json:"label,omitempty" jsonschema:"the phase the line names, as close; none when absent"`
	At    string `json:"at,omitempty" jsonschema:"HH:MM UTC the user gave for it; the clock's time when absent"`
}

// StatusSetOutput is status_set's result.
type StatusSetOutput struct {
	Written
	Status StatusView `json:"status"`
}

// statusSet rewrites the status line, or writes it under the title when the note has none.
func (d Deps) statusSet(ctx context.Context, in StatusSetInput) (StatusSetOutput, error) {
	at, err := d.clock(in.At)
	if err != nil {
		return StatusSetOutput{}, err
	}
	res, w, err := d.write(ctx, in.Date, "status_set", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		line, err := r.Status(note.StatusUnit{Time: at, Label: in.Label, Text: in.Text})
		if err != nil {
			return nil, "", err
		}
		headline := "status at " + at
		if n.Status == nil {
			return note.InsertBlock(src, n.Title.End, line), headline, nil
		}
		return note.Replace(src, n.Status.Span, line), headline, nil
	})
	if err != nil {
		return StatusSetOutput{}, err
	}
	s := res.Note.Status
	return StatusSetOutput{Written: w, Status: StatusView{
		Line: s.Line, Time: s.Time, Label: s.Label, Text: source(res.Src, s.Span),
	}}, nil
}
