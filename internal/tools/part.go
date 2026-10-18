package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// ErrNoPart is returned for a Part number a project does not have.
var ErrNoPart = errors.New("no such Part")

// partIcons are the icons a Part is written with (spec §4.2): running, in review, done.
var partIcons = []note.Icon{note.IconInProgress, note.IconInReview, note.IconDone}

// PartWriteInput is part_write's input: one Part, whole.
type PartWriteInput struct {
	Date  string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Repo  string `json:"repo" jsonschema:"its project, owner/repo; the note must have its section"`
	N     int    `json:"n" jsonschema:"the Part's number, 1 or more: rewrites Part n, or adds it before What remains, in number order"`
	Kind  string `json:"kind" jsonschema:"Human session, Agent batch or Land"`
	Title string `json:"title,omitempty" jsonschema:"one line after the kind, as: wave 1, running"`
	Icon  string `json:"icon" jsonschema:"🔄 while it runs, 🔍 while its work is in review, ✅ when it is done; written as given, never derived"`
	Body  string `json:"body,omitempty" jsonschema:"the markdown under its heading, replacing what it held: lines, a table, anchors (a paragraph ending ^<id>); no heading and no work item"`
}

// PartOutput is a Part where it now is.
type PartOutput struct {
	Repo string   `json:"repo" jsonschema:"its project"`
	Part PartView `json:"part"`
}

// PartWriteOutput is part_write's result.
type PartWriteOutput struct {
	Written
	Created bool `json:"created" jsonschema:"the Part is new"`
	PartOutput
}

// partWrite creates Part N of a project, or rewrites it whole.
func (d Deps) partWrite(ctx context.Context, in PartWriteInput) (PartWriteOutput, error) {
	icon := note.Icon(in.Icon)
	if !slices.Contains(partIcons, icon) {
		return PartWriteOutput{}, fmt.Errorf("part icon %q: want 🔄 (running), 🔍 (in review) or ✅ (done)", in.Icon)
	}
	if !slices.Contains(note.PartKinds, in.Kind) {
		return PartWriteOutput{}, fmt.Errorf("part kind %q: want one of %s", in.Kind, strings.Join(note.PartKinds, ", "))
	}
	if in.N < 1 {
		return PartWriteOutput{}, fmt.Errorf("part number %d: want 1 or more", in.N)
	}
	created := false
	res, w, err := d.write(ctx, in.Date, "part_write", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		p, err := needProject(n, in.Repo)
		if err != nil {
			return nil, "", err
		}
		if c := len(slices.DeleteFunc(slices.Clone(p.Parts), func(pt *note.Part) bool { return pt.N != in.N })); c > 1 {
			return nil, "", fmt.Errorf("%s has %d Parts numbered %d: which to rewrite is unclear", in.Repo, c, in.N)
		}
		unit, err := r.Part(note.PartUnit{Icon: icon, N: in.N, Kind: in.Kind, Title: strings.TrimSpace(in.Title), Body: in.Body})
		if err != nil {
			return nil, "", err
		}
		var out []byte
		if old := partOf(p, in.N); old != nil {
			out = note.Replace(src, old.Span, unit)
		} else {
			created = true
			out = note.InsertBlock(src, newPartAt(p, in.N), unit)
		}
		if err := checkPart(out, in.Repo, in.N, unit); err != nil {
			return nil, "", err
		}
		verb := "rewrite "
		if created {
			verb = "add "
		}
		return out, verb + partName(in.Repo, in.N) + " — " + in.Kind, nil
	})
	if err != nil {
		return PartWriteOutput{}, err
	}
	pt := partOf(projectOf(res.Note, in.Repo), in.N)
	return PartWriteOutput{Written: w, Created: created, PartOutput: PartOutput{Repo: in.Repo, Part: partView(res.Src, pt)}}, nil
}

// newPartAt is where Part number goes in project p: before its first Part numbered higher, else
// after its last Part, before What remains, else at the end of the section.
func newPartAt(p *note.Project, number int) int {
	for _, pt := range p.Parts {
		if pt.N > number {
			return pt.Start
		}
	}
	switch {
	case len(p.Parts) > 0:
		return p.Parts[len(p.Parts)-1].End
	case p.Remains != nil:
		return p.Remains.Start
	}
	return p.End
}

// checkPart checks that out, the note with unit written as Part number of repo, parses with
// that Part holding exactly unit's bytes and no work item: a heading in the body would end the
// Part early, and a finished Part holds plain lines (spec §4.2).
func checkPart(out []byte, repo string, number int, unit []byte) error {
	n, err := reparse(out)
	if err != nil {
		return err
	}
	pt := partOf(projectOf(n, repo), number)
	if pt == nil || string(out[pt.Start:pt.End]) != string(unit) {
		return fmt.Errorf("%s: the body would end the Part: it may hold no heading", partName(repo, number))
	}
	if len(pt.Items) > 0 {
		return fmt.Errorf("%s: ^%s would be a work item in a Part, which holds plain lines: "+
			"leave out its id, or keep it in its bucket and file it with item_transition's part", partName(repo, number), pt.Items[0].ID)
	}
	return nil
}

// fileItem moves work item id of n to DONE or DROPPED and files it into Part number of its
// project (spec §6): in one write it leaves its bucket and its *Next* lines, and becomes a plain
// line at the end of the Part, `- <icon> <title> — <summary>`, without its id, cost, route,
// sent status, prose, button or prompt.
func fileItem(n *note.Note, src []byte, r note.Renderer, id string, to note.State, summary string, number int) ([]byte, error) {
	if to != note.Done && to != note.Dropped {
		return nil, fmt.Errorf("^%s: only a move to DONE or DROPPED files an item into a Part, not to %s", id, to)
	}
	it := n.Item(id)
	if it == nil {
		if n.Action(id) != nil {
			return nil, fmt.Errorf("^%s is a manual action: ticking it leaves it where it is, so give no part", id)
		}
		return nil, fmt.Errorf("%w ^%s", note.ErrNoItem, id)
	}
	p := projectAt(n, it.Start)
	b := bucketAt(p, it.Start)
	if b == nil {
		return nil, fmt.Errorf("^%s is in a Part already", id)
	}
	if partOf(p, number) == nil {
		return nil, fmt.Errorf("^%s: %w: %s has no Part %d; part_write writes it first", id, ErrNoPart, p.Repo, number)
	}
	if strings.ContainsAny(summary, "\r\n") {
		return nil, fmt.Errorf("^%s: summary %q: want one line", id, summary)
	}
	summary = strings.TrimSpace(summary)
	from, _ := note.StateOf(it.Icon)
	if err := note.Transition(from, to, summary); err != nil {
		return nil, fmt.Errorf("^%s: %w", id, err)
	}
	if _, was := note.SplitSummary(it.Title); summary == "" {
		summary = was
	}
	title := it.Title
	if it.Sent != nil {
		title = it.Sent.Title // a filed ask is done with: its sent status goes with its id
	}
	line, err := r.Item(note.ItemUnit{Icon: to.Icon(), Title: note.Action(title), Summary: summary})
	if err != nil {
		return nil, fmt.Errorf("^%s: %w", id, err)
	}
	// Its Next lines go first, since a details link to an id the note lacks does not parse.
	out := src
	if n.Next != nil {
		for _, x := range slices.Backward(n.Next.Items) {
			if x.Ref == id {
				out = note.Remove(out, x.Span)
			}
		}
		if n, err = reparse(out); err != nil {
			return nil, err
		}
		it = n.Item(id)
		b = bucketAt(projectAt(n, it.Start), it.Start)
	}
	if out, err = removeItem(out, it, b); err != nil {
		return nil, err
	}
	if n, err = reparse(out); err != nil {
		return nil, err
	}
	pt := partOf(projectOf(n, p.Repo), number)
	return appendLine(out, pt.End, pt.Lists, line), nil
}

// partHeadline is a filed item's headline ending.
func partHeadline(number int) string { return ", into Part " + strconv.Itoa(number) }
