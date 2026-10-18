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
	CarryOver []CarryOverView `json:"carry_over" jsonschema:"the note's carry-over rows: for a new note, one per open item of the previous note, with no verdict yet"`
	Prepared  bool            `json:"prepared" jsonschema:"today's note was prepared ahead: it was kept, and gained only the rows it lacked"`
	Added     int             `json:"added" jsonschema:"the carry-over rows this open wrote"`
	Committed bool            `json:"committed" jsonschema:"the note was committed as one jj change"`
	Skipped   string          `json:"skipped,omitempty" jsonschema:"why the note was not committed"`
}

// dayOpen creates today's note from the template, carrying over every open item of the
// previous note. A note prepared ahead, one that exists and holds more than whitespace, is
// kept: it gains a row for each open item it does not carry yet, and its status line reads
// open (openPrepared).
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

	var rows []carriedRow
	if prevDate != "" {
		prev, err := note.Parse(prevSrc)
		if err != nil {
			return DayOpenOutput{}, fmt.Errorf("the previous note %s.md does not parse, so its open items "+
				"cannot all be collected; fix it first: %w", prevDate, err)
		}
		if rows, err = collect(prev, prevDate, r); err != nil {
			return DayOpenOutput{}, fmt.Errorf("the previous note %s.md: %w", prevDate, err)
		}
	}
	at := now.Format(note.ClockLayout)
	day := note.Day{Date: today, Previous: prevDate}
	day.CarryOver, day.Repos = split(rows)
	day.Status = note.StatusUnit{Time: at, Label: "open", Text: openStatus(prevDate, len(rows))}
	fresh, err := r.Note(day)
	if err != nil {
		return DayOpenOutput{}, err
	}

	var prepared bool
	var added int
	res, err := d.engine().Mutate(ctx, today, "day_open", func(n *note.Note, src []byte) ([]byte, string, error) {
		if n == nil { // the engine passes an empty note as nil, as a missing one
			prepared, added = false, len(rows)
			if prevDate == "" {
				return fresh, "open", nil
			}
			return fresh, fmt.Sprintf("open from %s, %d carry-over rows", prevDate, len(rows)), nil
		}
		out, headline, a, err := openPrepared(n, src, r, prevDate, at, rows)
		prepared, added = true, a
		return out, headline, err
	})
	if err != nil {
		return DayOpenOutput{}, err
	}

	o := DayOpenOutput{
		Path: res.Path, Date: today, Previous: prevDate, CarryOver: carryOverViews(res.Note),
		Prepared: prepared, Added: added, Committed: res.Committed, Skipped: res.Skipped,
	}
	if s := res.Note.Status; s != nil {
		o.Status = StatusView{Line: s.Line, Time: s.Time, Label: s.Label, Text: source(res.Src, s.Span)}
	}
	return o, nil
}

// openPrepared opens n, today's note prepared ahead, from src: it adds a carry-over row for
// each of rows, the previous note's (of date prev), that n does not carry yet (carriedIn),
// after its last row, and rewrites its status line as open at clock, HH:MM. A note with no
// carry-over table gains one, in its own section before Process when it has none. The front matter's
// projects list gains the repos of rows, as a new note's lists them. Nothing else is written:
// objectives, Next and buckets stay as prepared.
func openPrepared(n *note.Note, src []byte, r note.Renderer, prev, clock string, rows []carriedRow) ([]byte, string, int, error) {
	var add []carriedRow
	for _, c := range rows {
		if !carriedIn(n, c) {
			add = append(add, c)
		}
	}
	units, _ := split(add)
	_, repos := split(rows)

	out := src
	if len(units) > 0 { // the carry-over comes after the status line: splice it first
		var err error
		if out, err = addCarryOver(n, out, r, prev, units); err != nil {
			return nil, "", 0, err
		}
	}
	line, err := r.Status(note.StatusUnit{Time: clock, Label: "open", Text: preparedStatus(prev, len(add))})
	if err != nil {
		return nil, "", 0, err
	}
	if n.Status == nil {
		out = note.InsertBlock(out, n.Title.End, line)
	} else {
		out = note.Replace(out, n.Status.Span, line)
	}
	for _, repo := range repos {
		m, err := reparse(out)
		if err != nil {
			return nil, "", 0, err
		}
		if out, err = note.ListProject(out, m.FrontMatter, repo); err != nil {
			return nil, "", 0, err
		}
	}

	headline := "open the prepared note"
	if prev != "" {
		headline = fmt.Sprintf("open the prepared note from %s, %d carry-over rows added", prev, len(add))
	}
	return out, headline, len(add), nil
}

// addCarryOver splices units into n's carry-over table, from src: after its last row, or its
// header. A Carry-over section with no table gains one at its end; a note with no such
// section gains one before Process, or at its end.
func addCarryOver(n *note.Note, src []byte, r note.Renderer, prev string, units []note.CarryOverUnit) ([]byte, error) {
	c := n.CarryOver
	switch {
	case c != nil && c.Table != nil:
		b, err := r.CarryOverRows(units)
		if err != nil {
			return nil, err
		}
		at := c.Table.End
		if len(c.Rows) > 0 {
			at = c.Rows[len(c.Rows)-1].End
		}
		if at > 0 && src[at-1] != '\n' { // a table at the end of a note with no final newline
			b = append([]byte("\n"), b...)
		}
		return note.Insert(src, at, b), nil
	case c != nil:
		b, err := r.CarryOverTable(units)
		if err != nil {
			return nil, err
		}
		return note.InsertBlock(src, c.End, b), nil
	}
	b, err := r.CarryOverSection(prev, units)
	if err != nil {
		return nil, err
	}
	at := len(src)
	if n.Process != nil {
		at = n.Process.Start
	}
	return note.InsertBlock(src, at, b), nil
}

// preparedStatus is the status text of a note prepared ahead, once opened.
func preparedStatus(prev string, added int) string {
	switch {
	case prev == "":
		return "Opened the note prepared ahead. No previous note."
	case added == 0:
		return "Opened the note prepared ahead, from " + prev + ": it already carries every open item."
	case added == 1:
		return "Opened the note prepared ahead, from " + prev + ": 1 carry-over row added to triage."
	}
	return "Opened the note prepared ahead, from " + prev + ": " + strconv.Itoa(added) + " carry-over rows added to triage."
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
