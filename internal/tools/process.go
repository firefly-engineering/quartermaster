package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// ProcessSetInput is process_set's input.
type ProcessSetInput struct {
	Date   string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Entry  int    `json:"entry,omitempty" jsonschema:"the entry's 1-based place among Process's entries, as day_read lists them; absent adds one"`
	Text   string `json:"text,omitempty" jsonschema:"the entry, one line: a process change to make or keep; adds it, or rewords the entry"`
	Settle bool   `json:"settle,omitempty" jsonschema:"the entry is settled: it leaves Process, so the next day_open does not carry it over"`
}

// ProcessSetOutput is process_set's result.
type ProcessSetOutput struct {
	Written
	Process []EntryView `json:"process" jsonschema:"the Process list as written, without its - — placeholder"`
}

// processSet adds, rewords or settles a *Process* entry. An entry carries over to the next day
// until it is settled, which removes it: the jj history keeps it.
func (d Deps) processSet(ctx context.Context, in ProcessSetInput) (ProcessSetOutput, error) {
	if in.Settle && in.Text != "" {
		return ProcessSetOutput{}, errors.New("settle an entry or reword it, not both")
	}
	if in.Entry < 0 {
		return ProcessSetOutput{}, fmt.Errorf("entry %d: want 1 or more", in.Entry)
	}
	res, w, err := d.write(ctx, in.Date, "process_set", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		if in.Entry == 0 {
			if in.Settle {
				return nil, "", errors.New("settle names its entry")
			}
			line, err := r.ProcessLine(in.Text)
			if err != nil {
				return nil, "", err
			}
			return appendToProcess(n, src, r, line), "add: " + in.Text, nil
		}
		if n.Process == nil || in.Entry > len(n.Process.Entries) {
			count := 0
			if n.Process != nil {
				count = len(n.Process.Entries)
			}
			return nil, "", fmt.Errorf("no Process entry %d: Process has %d", in.Entry, count)
		}
		e := n.Process.Entries[in.Entry-1]
		name := "entry " + strconv.Itoa(in.Entry)
		if in.Settle {
			out, err := removeLine(n, src, e.Span)
			return out, "settle " + name + ": " + e.Text, err
		}
		if lineEnd(src, e.Start) != e.End {
			return nil, "", fmt.Errorf("process %s spans several lines, which a reword would lose: settle it and add it anew", name)
		}
		line, err := r.ProcessLine(in.Text)
		if err != nil {
			return nil, "", err
		}
		return note.Replace(src, e.Span, line), "reword " + name + ": " + in.Text, nil
	})
	if err != nil {
		return ProcessSetOutput{}, err
	}
	o := ProcessSetOutput{Written: w, Process: []EntryView{}}
	if res.Note.Process != nil {
		for _, e := range res.Note.Process.Entries {
			o.Process = append(o.Process, EntryView{Line: e.Line, Text: e.Text})
		}
	}
	return o, nil
}
