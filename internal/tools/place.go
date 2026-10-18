package tools

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
)

// A place is where a line unit goes: a project's bucket or Part, or *Process*.

// projectOf is the section of project repo in n, or nil.
func projectOf(n *note.Note, repo string) *note.Project {
	i := slices.IndexFunc(n.Projects, func(p *note.Project) bool { return p.Repo == repo })
	if i < 0 {
		return nil
	}
	return n.Projects[i]
}

// partOf is Part number of project p, or nil.
func partOf(p *note.Project, number int) *note.Part {
	i := slices.IndexFunc(p.Parts, func(pt *note.Part) bool { return pt.N == number })
	if i < 0 {
		return nil
	}
	return p.Parts[i]
}

// needProject is project repo of n, or an error naming the tool that creates its section.
func needProject(n *note.Note, repo string) (*note.Project, error) {
	if !note.ValidRepo(repo) {
		return nil, fmt.Errorf("repo %q: want owner/repo", repo)
	}
	if p := projectOf(n, repo); p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("the note has no section for %s: item_upsert creates it with its first work item", repo)
}

// appendLine writes line at end, the end of a section whose top-level lists are lists: joining
// the last one when the section ends with it, else as a block of its own.
func appendLine(src []byte, end int, lists []note.Span, line []byte) []byte {
	if len(lists) > 0 && lists[len(lists)-1].End == end {
		return note.Insert(src, end, line)
	}
	return note.InsertBlock(src, end, line)
}

// appendToBucket writes line at the end of bucket b, in place of its `- —` when it holds
// nothing else. A block, as a ##### item, never joins a list.
func appendToBucket(src []byte, b *note.Bucket, line []byte, block bool) []byte {
	if len(b.Lists) == 1 && len(b.Dashes) == 1 && b.Lists[0] == b.Dashes[0] && b.Lists[0].End == b.End &&
		len(b.Items) == 0 && len(b.Entries) == 0 {
		return note.Replace(src, b.Dashes[0], line)
	}
	if block {
		return note.InsertBlock(src, b.End, line)
	}
	return appendLine(src, b.End, b.Lists, line)
}

// newBucketAt is where bucket, missing from What remains r, goes: before the first bucket after it
// in template order, else at the end of r.
func newBucketAt(r *note.Remains, bucket string) int {
	order := slices.Index(note.BucketNames, bucket)
	for _, b := range r.Buckets {
		if slices.Index(note.BucketNames, b.Name) > order {
			return b.Start
		}
	}
	return r.End
}

// ensureBucket returns src with bucket in project repo, adding What remains (every bucket, in
// template order) or the bucket when the project lacks it, and the note parsed from it. The
// project must have its section.
func ensureBucket(n *note.Note, src []byte, r note.Renderer, repo, bucket string) ([]byte, *note.Note, *note.Bucket, error) {
	if !slices.Contains(note.BucketNames, bucket) {
		return nil, nil, nil, fmt.Errorf("unknown bucket %q: want one of %s", bucket, strings.Join(note.BucketNames, ", "))
	}
	p, err := needProject(n, repo)
	if err != nil {
		return nil, nil, nil, err
	}
	var add []byte
	at := 0
	switch {
	case p.Remains == nil:
		var all []note.BucketUnit
		for _, name := range note.BucketNames {
			all = append(all, note.BucketUnit{Icon: note.IconNone, Name: name})
		}
		add, err = r.Remains(note.IconNone, all)
		at = p.End
	case !slices.ContainsFunc(p.Remains.Buckets, func(b *note.Bucket) bool { return b.Name == bucket }):
		add, err = r.Bucket(note.BucketUnit{Icon: note.IconNone, Name: bucket})
		at = newBucketAt(p.Remains, bucket)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if add != nil {
		src = note.InsertBlock(src, at, add)
		if n, err = reparse(src); err != nil {
			return nil, nil, nil, err
		}
		p = projectOf(n, repo)
	}
	i := slices.IndexFunc(p.Remains.Buckets, func(b *note.Bucket) bool { return b.Name == bucket })
	return src, n, p.Remains.Buckets[i], nil
}

// appendToProcess writes line at the end of Process, in place of its `- —` when that is all
// it holds, adding the section at the end of the note when it has none.
func appendToProcess(n *note.Note, src []byte, r note.Renderer, line []byte) []byte {
	pr := n.Process
	switch {
	case pr == nil:
		return note.InsertBlock(src, len(src), r.ProcessSection(line))
	case pr.List == nil:
		return note.InsertBlock(src, pr.End, line)
	case string(src[pr.List.Start:pr.List.End]) == note.NothingLine:
		return note.Replace(src, *pr.List, line)
	}
	return note.Insert(src, pr.List.End, line)
}

// removeLine removes the list item at s from n's source src, putting back `- —` when it leaves
// its bucket or Process with nothing.
func removeLine(n *note.Note, src []byte, s note.Span) ([]byte, error) {
	var b *note.Bucket
	if p := projectAt(n, s.Start); p != nil {
		b = bucketAt(p, s.Start)
	}
	inProcess := n.Process != nil && s.Start >= n.Process.Start && s.Start < n.Process.End
	out := note.Remove(src, s)
	m, err := reparse(out)
	if err != nil {
		return nil, err
	}
	if inProcess && m.Process != nil && m.Process.List == nil {
		return note.InsertBlock(out, m.Process.End, []byte(note.NothingLine)), nil
	}
	if b == nil {
		return out, nil
	}
	for _, p := range m.Projects {
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

// partName names Part number of project repo, as errors and headlines do.
func partName(repo string, number int) string { return repo + ", Part " + strconv.Itoa(number) }
