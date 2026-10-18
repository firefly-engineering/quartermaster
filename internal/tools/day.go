package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// shellCommandsData is the Shell commands plugin's settings, relative to the vault.
var shellCommandsData = filepath.Join(".obsidian", "plugins", "obsidian-shellcommands", "data.json")

// DayOpenInput is day_open's input: none, today is the clock's.
type DayOpenInput struct{}

// DayOpenOutput is day_open's result.
type DayOpenOutput struct {
	Path      string          `json:"path" jsonschema:"the new note's path"`
	Date      string          `json:"date" jsonschema:"today, YYYY-MM-DD, UTC"`
	Previous  string          `json:"previous,omitempty" jsonschema:"the previous note's date, absent when there is none"`
	Status    StatusView      `json:"status"`
	CarryOver []CarryOverView `json:"carry_over" jsonschema:"one row per open item of the previous note, with no verdict yet"`
	Committed bool            `json:"committed" jsonschema:"the note was committed as one jj change"`
	Skipped   string          `json:"skipped,omitempty" jsonschema:"why the note was not committed"`
}

// dayOpen creates today's note from the template, carrying over every open item of the
// previous note. A note that exists and holds more than whitespace is refused.
func (d Deps) dayOpen(ctx context.Context, _ DayOpenInput) (DayOpenOutput, error) {
	vault, err := d.vault()
	if err != nil {
		return DayOpenOutput{}, err
	}
	now := d.today()
	today := now.Format(time.DateOnly)

	prevDate, prevSrc, err := previousNote(vault, today)
	if err != nil {
		return DayOpenOutput{}, err
	}
	id, err := shellCommandID(vault)
	if err != nil {
		return DayOpenOutput{}, err
	}
	r := note.Renderer{Vault: d.Config.Vault.Name, Date: today, CommandID: id}

	day := note.Day{Date: today, Previous: prevDate}
	if prevDate != "" {
		prev, err := note.Parse(prevSrc)
		if err != nil {
			return DayOpenOutput{}, fmt.Errorf("the previous note %s.md does not parse, so its open items "+
				"cannot all be collected; fix it first: %w", prevDate, err)
		}
		if day.CarryOver, err = carryOver(prev, prevDate, r); err != nil {
			return DayOpenOutput{}, fmt.Errorf("the previous note %s.md: %w", prevDate, err)
		}
	}
	day.Status = note.StatusUnit{Time: now.Format(note.ClockLayout), Label: "open", Text: openStatus(prevDate, len(day.CarryOver))}

	out, err := r.Note(day)
	if err != nil {
		return DayOpenOutput{}, err
	}
	headline := "open"
	if prevDate != "" {
		headline = fmt.Sprintf("open from %s, %d carry-over rows", prevDate, len(day.CarryOver))
	}

	res, err := d.engine().Mutate(ctx, today, "day_open", func(n *note.Note, _ []byte) ([]byte, string, error) {
		if n != nil { // the engine passes an empty note as nil, as a missing one
			return nil, "", fmt.Errorf("today's note %s.md exists and is not empty; day_open never overwrites a note", today)
		}
		return out, headline, nil
	})
	if err != nil {
		return DayOpenOutput{}, err
	}

	o := DayOpenOutput{
		Path: res.Path, Date: today, Previous: prevDate, CarryOver: carryOverViews(res.Note),
		Committed: res.Committed, Skipped: res.Skipped,
	}
	if s := res.Note.Status; s != nil {
		line, err := note.Renderer{}.Status(day.Status)
		if err != nil {
			return DayOpenOutput{}, err
		}
		o.Status = StatusView{Line: s.Line, Time: s.Time, Label: s.Label, Text: strings.TrimSuffix(string(line), "\n")}
	}
	return o, nil
}

// openStatus is the new note's status text.
func openStatus(prev string, rows int) string {
	if prev == "" {
		return "Opened. No previous note: set the day's objectives."
	}
	switch rows {
	case 0:
		return "Opened from " + prev + ", which left nothing open: set the day's objectives."
	case 1:
		return "Opened from " + prev + ": 1 carry-over row to triage, then set the day's objectives."
	}
	return "Opened from " + prev + ": " + strconv.Itoa(rows) +
		" carry-over rows to triage, then set the day's objectives."
}

// previousNote finds the latest dated note in vault before today, `YYYY-MM-DD.md`, and returns
// its date and source. An empty or whitespace-only note counts as missing, as the engine
// counts it. date is "" when there is none.
func previousNote(vault, today string) (date string, src []byte, err error) {
	entries, err := os.ReadDir(vault)
	if err != nil {
		return "", nil, err
	}
	// ReadDir sorts by name, and a YYYY-MM-DD name sorts by date: walk back from the end.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		name, ok := strings.CutSuffix(e.Name(), ".md")
		if !ok || !e.Type().IsRegular() || !note.IsDate(name) || name >= today {
			continue
		}
		b, err := os.ReadFile(filepath.Join(vault, e.Name()))
		if err != nil {
			return "", nil, err
		}
		if !note.Empty(b) {
			return name, b, nil
		}
	}
	return "", nil, nil
}

// shellCommandID is the ▶ buttons' command id from the vault's Shell commands settings, ""
// when the plugin is not set up: the note then has no buttons.
func shellCommandID(vault string) (string, error) {
	b, err := os.ReadFile(filepath.Join(vault, shellCommandsData))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	id, _ := note.ShellCommandID(b)
	return id, nil
}

// DayReadInput is day_read's input.
type DayReadInput struct {
	Date string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
}

// DayReadOutput is day_read's result.
type DayReadOutput struct {
	Path   string           `json:"path" jsonschema:"the note's path"`
	Note   NoteView         `json:"note" jsonschema:"every unit the parser could place"`
	Errors []ParseErrorView `json:"errors" jsonschema:"the units it could not, by line; empty when the note parses"`
}

// dayRead returns a note as structured data, with what failed to parse. It never writes.
func (d Deps) dayRead(_ context.Context, in DayReadInput) (DayReadOutput, error) {
	vault, err := d.vault()
	if err != nil {
		return DayReadOutput{}, err
	}
	date := in.Date
	if date == "" {
		date = d.today().Format(time.DateOnly)
	}
	if !note.IsDate(date) {
		return DayReadOutput{}, fmt.Errorf("not a date: %q, want YYYY-MM-DD", date)
	}
	path := filepath.Join(vault, date+".md")
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && note.Empty(src) {
		return DayReadOutput{}, fmt.Errorf("no note for %s at %s: day_open creates today's", date, path)
	}
	if err != nil {
		return DayReadOutput{}, err
	}
	n, perr := note.Parse(src)
	return DayReadOutput{Path: path, Note: view(n, src), Errors: parseErrors(perr)}, nil
}
