package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/firefly-engineering/quartermaster/internal/herdr"
	"github.com/firefly-engineering/quartermaster/internal/note"
	"github.com/firefly-engineering/quartermaster/internal/refresh"
)

// RefreshInput is refresh's input.
type RefreshInput struct {
	Date string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
}

// RefreshOutput is refresh's result.
type RefreshOutput struct {
	Written
	Applied      []refresh.Applied `json:"applied" jsonschema:"what the evidence proved and refresh changed, in note order"`
	SkippedItems []refresh.Skipped `json:"skipped_items" jsonschema:"the work items refresh checked and left as they were, and why"`
}

// refresh reconciles the note with GitHub and herdr (spec §6, §11). It asks them before it
// takes the note's lock, from a read of the note, then applies what they proved in one write;
// that write also ticks paired checkboxes and derives icons, as every write does, so a refresh
// that proves nothing still settles the note.
func (d Deps) refresh(ctx context.Context, in RefreshInput) (RefreshOutput, error) {
	return d.refreshWith(ctx, in, d.reconciler())
}

// reconciler asks GitHub, herdr, jj and git through the commands d resolved.
func (d Deps) reconciler() refresh.Reconciler {
	return refresh.Reconciler{
		GH: refresh.Command("gh", d.GH, d.Env), Herdr: herdr.New(d.Herdr, d.Env),
		JJ: refresh.Command("jj", d.JJ, d.Env), Git: refresh.Command("git", d.Git, d.Env),
		Projects: d.Config.Projects,
	}
}

// refreshWith is refresh asking GitHub, herdr, jj and git through rec, which tests stand fakes in.
func (d Deps) refreshWith(ctx context.Context, in RefreshInput, rec refresh.Reconciler) (RefreshOutput, error) {
	vault, err := d.vault()
	if err != nil {
		return RefreshOutput{}, err
	}
	date := in.Date
	if date == "" {
		date = d.today().Format(time.DateOnly)
	}
	if !note.IsDate(date) {
		return RefreshOutput{}, fmt.Errorf("not a date: %q, want YYYY-MM-DD", date)
	}
	src, err := os.ReadFile(filepath.Join(vault, date+".md"))
	if errors.Is(err, fs.ErrNotExist) || err == nil && note.Empty(src) {
		return RefreshOutput{}, fmt.Errorf("no note for %s: day_open creates today's", date)
	}
	if err != nil {
		return RefreshOutput{}, err
	}
	n, err := note.Parse(src)
	if err != nil {
		return RefreshOutput{}, fmt.Errorf("the note does not parse: %w", err)
	}
	plan := rec.Gather(ctx, n)

	var applied []refresh.Applied
	var skipped []refresh.Skipped
	_, w, err := d.write(ctx, date, "refresh", func(_ *note.Note, src []byte, _ note.Renderer) ([]byte, string, error) {
		out, a, s, err := plan.Apply(src)
		applied, skipped = a, s
		return out, headline(a), err
	})
	if err != nil {
		return RefreshOutput{}, err
	}
	if applied == nil {
		applied = []refresh.Applied{}
	}
	if skipped == nil {
		skipped = []refresh.Skipped{}
	}
	return RefreshOutput{Written: w, Applied: applied, SkippedItems: skipped}, nil
}

// headline sums up a refresh's changes for its commit message.
func headline(applied []refresh.Applied) string {
	if len(applied) == 0 {
		return "no evidence of a change"
	}
	var parts []string
	for _, a := range applied {
		part := "^" + a.Item
		if a.From != a.To {
			part += " " + a.From + " to " + a.To
		}
		if a.Status != "" {
			part += " " + a.Status
		}
		switch {
		case a.Workspace != "":
			part += " checkout removed"
		case a.Tab != "":
			part += " tab " + a.Tab + " closed"
		case a.Pane != "":
			part += " pane " + a.Pane + " dropped"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}
