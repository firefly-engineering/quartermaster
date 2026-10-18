package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// ItemFields are a work item as item_upsert writes it, and as carryover_verdict's keep does.
type ItemFields struct {
	ID     string       `json:"id" jsonschema:"the item id: lowercase letters, digits and -, unique in the note"`
	Repo   string       `json:"repo,omitempty" jsonschema:"its project, owner/repo; needed to create, unchanged when absent"`
	Bucket string       `json:"bucket,omitempty" jsonschema:"its bucket: Decisions for you, Your actions, Agent queue, Sent to other repos, Outside input: chase or Waiting; needed to create, unchanged when absent"`
	Title  string       `json:"title,omitempty" jsonschema:"one line, after its icon and ref link, without the separator ' — '; needed to create"`
	Ref    string       `json:"ref,omitempty" jsonschema:"the issue or PR it is about, owner/repo#N, or #N in its project; rendered as a full link"`
	Cost   string       `json:"cost,omitempty" jsonschema:"what it costs the user, as: 20 min"`
	Route  string       `json:"route,omitempty" jsonschema:"the next skill that moves it, as: /implement"`
	Body   string       `json:"body,omitempty" jsonschema:"prose under its heading, with no blank line; makes it a ##### item"`
	Prompt *PromptInput `json:"prompt,omitempty" jsonschema:"the prompt its ▶ button sends; makes it a ##### item"`
	Button string       `json:"button,omitempty" jsonschema:"the ▶ label, as: grill in trunk; with a prompt, send to <to> when absent"`
}

// PromptInput is a work item's herdr-prompt block.
type PromptInput struct {
	To   string `json:"to" jsonschema:"the herdr workspace label of the session it goes to"`
	Text string `json:"text" jsonschema:"the self-contained prompt"`
}

// ItemUpsertInput is item_upsert's input. An update rewrites the item from these fields; its
// state, summary and place, unless repo or bucket move it, stay as they are. It refuses to drop
// a ##### item's body and prompt by leaving them out.
type ItemUpsertInput struct {
	Date string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	ItemFields
	State string `json:"state,omitempty" jsonschema:"a new item's state, TODO when absent; item_transition moves an existing one"`
}

// ItemOutput is a work item where it now is.
type ItemOutput struct {
	Repo   string   `json:"repo" jsonschema:"its project"`
	Bucket string   `json:"bucket" jsonschema:"its bucket, absent in a Part"`
	Item   ItemView `json:"item"`
}

// ItemUpsertOutput is item_upsert's result.
type ItemUpsertOutput struct {
	Written
	Created bool `json:"created" jsonschema:"the item is new"`
	ItemOutput
}

// itemUpsert creates a work item in a bucket, or rewrites one in place or into another bucket.
func (d Deps) itemUpsert(ctx context.Context, in ItemUpsertInput) (ItemUpsertOutput, error) {
	created := false
	res, w, err := d.write(ctx, in.Date, "item_upsert", func(n *note.Note, src []byte, r note.Renderer) ([]byte, string, error) {
		it := n.Item(in.ID)
		created = it == nil
		if created {
			s := note.Todo
			if in.State != "" {
				var err error
				if s, err = note.ParseState(in.State); err != nil {
					return nil, "", err
				}
			}
			out, err := createItem(n, src, r, in.ItemFields, s.Icon(), "")
			return out, "add ^" + in.ID + " to " + in.Repo + ", " + in.Bucket, err
		}
		if in.State != "" {
			return nil, "", fmt.Errorf("^%s exists: item_transition moves it, item_upsert keeps its state", in.ID)
		}
		return updateItem(n, src, r, it, in.ItemFields)
	})
	if err != nil {
		return ItemUpsertOutput{}, err
	}
	return ItemUpsertOutput{Written: w, Created: created, ItemOutput: itemOutput(res.Note, in.ID)}, nil
}

// itemOutput is work item id of n, where it is.
func itemOutput(n *note.Note, id string) ItemOutput {
	it := n.Item(id)
	o := ItemOutput{Item: itemView(it)}
	if p := projectAt(n, it.Start); p != nil {
		o.Repo = p.Repo
		if b := bucketAt(p, it.Start); b != nil {
			o.Bucket = b.Name
		}
	}
	return o
}

// unit builds the renderer's unit for f in project repo, with its icon and summary.
func unit(f ItemFields, repo string, icon note.Icon, summary string) (note.ItemUnit, error) {
	u := note.ItemUnit{
		ID: f.ID, Icon: icon, Title: strings.TrimSpace(f.Title), Repo: repo, Cost: f.Cost, Route: f.Route,
		Summary: summary, Body: f.Body, Heading: f.Body != "" || f.Prompt != nil,
	}
	if f.Ref != "" {
		ref, err := note.ParseRef(f.Ref, repo)
		if err != nil {
			return note.ItemUnit{}, err
		}
		u.Ref = ref
	}
	if f.Prompt != nil {
		u.Prompt = &note.PromptUnit{To: f.Prompt.To, Body: f.Prompt.Text}
		u.Button = f.Button
		if u.Button == "" {
			u.Button = note.DefaultButton(f.Prompt.To)
		}
	} else if f.Button != "" {
		return note.ItemUnit{}, fmt.Errorf("^%s: a button needs a prompt to send", f.ID)
	}
	return u, nil
}

// createItem writes f as a new work item with icon and summary at the end of its bucket,
// creating the project section, its What remains or the bucket when the note lacks them.
func createItem(n *note.Note, src []byte, r note.Renderer, f ItemFields, icon note.Icon, summary string) ([]byte, error) {
	if !note.ValidID(f.ID) {
		return nil, fmt.Errorf("invalid item id ^%s: lowercase letters, digits and - only", f.ID)
	}
	if f.Repo == "" || f.Bucket == "" || strings.TrimSpace(f.Title) == "" {
		return nil, fmt.Errorf("a new work item ^%s needs its repo, bucket and title", f.ID)
	}
	if usedID(n, f.ID) {
		return nil, fmt.Errorf("^%s already names a manual action or anchor in the note", f.ID)
	}
	if !slices.Contains(note.BucketNames, f.Bucket) {
		return nil, fmt.Errorf("unknown bucket %q: want one of %s", f.Bucket, strings.Join(note.BucketNames, ", "))
	}
	u, err := unit(f, f.Repo, icon, summary)
	if err != nil {
		return nil, err
	}
	return insertItem(n, src, r, f.Repo, f.Bucket, u)
}

// usedID reports whether id names a manual action or an anchor.
func usedID(n *note.Note, id string) bool {
	return slices.ContainsFunc(n.Actions, func(a note.ManualAction) bool { return a.ID == id }) ||
		slices.ContainsFunc(n.Anchors, func(a note.Anchor) bool { return a.ID == id })
}

// updateItem rewrites work item it from f, keeping its icon and summary. In its bucket it is
// rewritten in place; a list item keeps its marker, indent and nested lines. Given another
// repo or bucket, or a change of form, it moves to the end of its bucket.
func updateItem(n *note.Note, src []byte, r note.Renderer, it *note.Item, f ItemFields) ([]byte, string, error) {
	p := projectAt(n, it.Start)
	b := bucketAt(p, it.Start)
	if b == nil {
		return nil, "", fmt.Errorf("^%s is in a Part, which records what was done: item_upsert writes items in buckets", f.ID)
	}
	if f.Repo == "" {
		f.Repo = p.Repo
	}
	if f.Bucket == "" {
		f.Bucket = b.Name
	}
	if strings.TrimSpace(f.Title) == "" {
		return nil, "", fmt.Errorf("^%s: an update rewrites the item, so it needs its title", f.ID)
	}
	_, summary := note.SplitSummary(it.Title)
	u, err := unit(f, f.Repo, it.Icon, summary)
	if err != nil {
		return nil, "", err
	}
	u.Dispatch = it.Dispatch // task_dispatch's record, which item_upsert does not take
	// An update rewrites the item whole, and the note is all there is of its prose and prompt.
	if it.Heading && !u.Heading {
		return nil, "", fmt.Errorf("^%s is a ##### item, which an update rewrites: give its body or prompt, or it would lose them", f.ID)
	}
	if it.Prompt != nil && u.Prompt == nil {
		return nil, "", fmt.Errorf("^%s has a prompt, which an update rewrites: give it again, or it would be lost", f.ID)
	}
	if f.Repo == p.Repo && f.Bucket == b.Name && u.Heading == it.Heading {
		if it.Heading {
			out, err := r.Item(u)
			return note.Replace(src, it.Span, out), "update ^" + f.ID, err
		}
		out, err := r.ItemTitle(u)
		return note.Replace(src, note.Span{Start: it.IconSpan.Start, End: it.Head.End}, out), "update ^" + f.ID, err
	}
	if !it.Heading && it.Head.End != it.End {
		return nil, "", fmt.Errorf("^%s has nested lines, which a move would lose: move it by hand", f.ID)
	}
	if !slices.Contains(note.BucketNames, f.Bucket) {
		return nil, "", fmt.Errorf("unknown bucket %q: want one of %s", f.Bucket, strings.Join(note.BucketNames, ", "))
	}
	out, err := removeItem(src, it, b)
	if err != nil {
		return nil, "", err
	}
	if n, err = reparse(out); err != nil {
		return nil, "", err
	}
	out, err = insertItem(n, out, r, f.Repo, f.Bucket, u)
	return out, "move ^" + f.ID + " to " + f.Repo + ", " + f.Bucket, err
}

// removeItem removes work item it from bucket b, putting back `- —` when it leaves b with nothing.
func removeItem(src []byte, it *note.Item, b *note.Bucket) ([]byte, error) {
	out := note.Remove(src, it.Span)
	n, err := reparse(out)
	if err != nil {
		return nil, err
	}
	for _, p := range n.Projects {
		if p.Remains == nil {
			continue
		}
		for _, nb := range p.Remains.Buckets {
			if nb.Start == b.Start && nb.End == nb.Heading.End {
				return note.InsertBlock(out, nb.End, []byte(note.NothingLine)), nil
			}
		}
	}
	return out, nil
}

// insertItem writes u at the end of bucket of project repo, in place of its `- —` when it has
// nothing else. A note without the project gets its section, after the last project, and lists
// it in its front matter; a project
// without What remains or the bucket gets it, the buckets in template order.
func insertItem(n *note.Note, src []byte, r note.Renderer, repo, bucket string, u note.ItemUnit) ([]byte, error) {
	var buckets []note.BucketUnit // every bucket, u in its own, for a new section
	for _, name := range note.BucketNames {
		b := note.BucketUnit{Icon: note.IconNone, Name: name}
		if name == bucket {
			b.Icon, b.Items = u.Icon, []note.ItemUnit{u}
		}
		buckets = append(buckets, b)
	}
	i := slices.IndexFunc(n.Projects, func(p *note.Project) bool { return p.Repo == repo })
	if i < 0 {
		section, err := r.Project(note.ProjectUnit{Icon: note.IconTodo, Repo: repo, Remains: note.IconTodo, Buckets: buckets})
		if err != nil {
			return nil, err
		}
		// The section goes after the front matter, so n's span of it still holds.
		return note.ListProject(note.InsertBlock(src, newProjectAt(n, src), section), n.FrontMatter, repo)
	}
	p := n.Projects[i]
	if p.Remains == nil {
		remains, err := r.Remains(note.IconTodo, buckets)
		if err != nil {
			return nil, err
		}
		return note.InsertBlock(src, p.End, remains), nil
	}
	k := slices.IndexFunc(p.Remains.Buckets, func(b *note.Bucket) bool { return b.Name == bucket })
	if k < 0 {
		b, err := r.Bucket(note.BucketUnit{Icon: u.Icon, Name: bucket, Items: []note.ItemUnit{u}})
		if err != nil {
			return nil, err
		}
		at := p.Remains.End
		order := slices.Index(note.BucketNames, bucket)
		for _, nb := range p.Remains.Buckets {
			if slices.Index(note.BucketNames, nb.Name) > order {
				at = nb.Start
				break
			}
		}
		return note.InsertBlock(src, at, b), nil
	}
	b := p.Remains.Buckets[k]
	line, err := r.Item(u)
	if err != nil {
		return nil, err
	}
	// b holds nothing but `- —`: u takes its place.
	if len(b.Lists) == 1 && len(b.Dashes) == 1 && b.Lists[0] == b.Dashes[0] && b.Lists[0].End == b.End &&
		len(b.Items) == 0 && len(b.Entries) == 0 {
		return note.Replace(src, b.Dashes[0], line), nil
	}
	// A list item joins the list b ends with; anything else is a block of its own.
	if !u.Heading && len(b.Lists) > 0 && b.Lists[len(b.Lists)-1].End == b.End {
		return note.Insert(src, b.End, line), nil
	}
	return note.InsertBlock(src, b.End, line), nil
}

// newProjectAt is where a new project section goes: after the last one, else before
// Carry-over, else before Process, else at the end.
func newProjectAt(n *note.Note, src []byte) int {
	switch {
	case len(n.Projects) > 0:
		return n.Projects[len(n.Projects)-1].End
	case n.CarryOver != nil:
		return n.CarryOver.Start
	case n.Process != nil:
		return n.Process.Start
	}
	return len(src)
}

// ItemTransitionInput is item_transition's input.
type ItemTransitionInput struct {
	Date    string `json:"date,omitempty" jsonschema:"the note's date, YYYY-MM-DD; today (UTC) when absent"`
	Item    string `json:"item" jsonschema:"the work item's id"`
	To      string `json:"to" jsonschema:"the state to move it to: TODO, IN_PROGRESS, IN_REVIEW, WAITING, DONE or DROPPED"`
	Summary string `json:"summary,omitempty" jsonschema:"one line: the PR URL, commit, evidence or reason; needed to wait, to drop, and to complete from IN_PROGRESS; the item's last one stays when absent"`
}

// ItemTransitionOutput is item_transition's result.
type ItemTransitionOutput struct {
	Written
	From   string        `json:"from" jsonschema:"the state it left"`
	To     string        `json:"to" jsonschema:"the state it is in"`
	Item   *ItemOutput   `json:"item,omitempty" jsonschema:"the work item, absent for a manual action"`
	Action *ManualAction `json:"action,omitempty" jsonschema:"the manual action ticked, absent for a work item"`
}

// itemTransition moves one work item through spec §4.3, or ticks one manual action.
func (d Deps) itemTransition(ctx context.Context, in ItemTransitionInput) (ItemTransitionOutput, error) {
	to, err := note.ParseState(in.To)
	if err != nil {
		return ItemTransitionOutput{}, err
	}
	var from note.State
	res, w, err := d.write(ctx, in.Date, "item_transition", func(n *note.Note, src []byte, _ note.Renderer) ([]byte, string, error) {
		if it := n.Item(in.Item); it != nil {
			from, _ = note.StateOf(it.Icon)
		} else if a := n.Action(in.Item); a != nil {
			from = a.State()
		}
		out, err := note.TransitionItem(n, src, in.Item, to, in.Summary)
		headline := "^" + in.Item + " " + from.String() + " to " + to.String()
		if s := strings.TrimSpace(in.Summary); s != "" {
			headline += ": " + s
		}
		return out, headline, err
	})
	if err != nil {
		return ItemTransitionOutput{}, err
	}
	o := ItemTransitionOutput{Written: w, From: from.String(), To: to.String()}
	if res.Note.Item(in.Item) != nil {
		io := itemOutput(res.Note, in.Item)
		o.Item = &io
	} else if a := res.Note.Action(in.Item); a != nil {
		o.Action = &ManualAction{Line: a.Line, ID: a.ID, Text: a.Text, Checked: a.Checked}
	} else {
		return ItemTransitionOutput{}, errors.New("the item is gone from the note it was written to")
	}
	return o, nil
}
