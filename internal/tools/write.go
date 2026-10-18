package tools

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/engine"
	"github.com/firefly-engineering/quartermaster/internal/note"
)

// Written is what every write tool returns besides the units it changed.
type Written struct {
	Path      string `json:"path" jsonschema:"the note's path"`
	Changed   bool   `json:"changed" jsonschema:"false when the note already read so and nothing was written"`
	Committed bool   `json:"committed" jsonschema:"the change was committed as one jj change"`
	Skipped   string `json:"skipped,omitempty" jsonschema:"why the change was not committed"`
}

// edit is a write tool's one change to a note: n parsed from src, and r renders its units.
type edit func(n *note.Note, src []byte, r note.Renderer) (out []byte, headline string, err error)

// write runs e on the note of date, today (UTC) when "", through the engine as tool. The note
// must exist: day_open creates it.
func (d Deps) write(ctx context.Context, date, tool string, e edit) (engine.Result, Written, error) {
	vault, err := d.vault()
	if err != nil {
		return engine.Result{}, Written{}, err
	}
	if date == "" {
		date = d.today().Format(time.DateOnly)
	}
	if !note.IsDate(date) {
		return engine.Result{}, Written{}, fmt.Errorf("not a date: %q, want YYYY-MM-DD", date)
	}
	id, err := shellCommandID(vault)
	if err != nil {
		return engine.Result{}, Written{}, err
	}
	r := note.Renderer{Vault: d.Config.Vault.Name, Date: date, CommandID: id}
	res, err := d.engine().Mutate(ctx, date, tool, func(n *note.Note, src []byte) ([]byte, string, error) {
		if n == nil {
			return nil, "", fmt.Errorf("no note for %s: day_open creates today's", date)
		}
		return e(n, src, r)
	})
	if err != nil {
		return engine.Result{}, Written{}, err
	}
	return res, Written{Path: res.Path, Changed: res.Changed, Committed: res.Committed, Skipped: res.Skipped}, nil
}

// clock is the time a write records, HH:MM UTC: at when given, else the clock's.
func (d Deps) clock(at string) (string, error) {
	if at == "" {
		return d.today().Format(note.ClockLayout), nil
	}
	if t, err := time.Parse(note.ClockLayout, at); err != nil || t.Format(note.ClockLayout) != at {
		return "", fmt.Errorf("at %q: want HH:MM, UTC", at)
	}
	return at, nil
}

// reparse parses an edit's intermediate source, which must parse as the engine's would.
func reparse(src []byte) (*note.Note, error) {
	n, err := note.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("the edit would leave a note that does not parse: %w", err)
	}
	return n, nil
}

// lineEnd is the offset past the newline ending the line at off.
func lineEnd(src []byte, off int) int {
	if i := bytes.IndexByte(src[off:], '\n'); i >= 0 {
		return off + i + 1
	}
	return len(src)
}
