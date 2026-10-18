package refresh

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/firefly-engineering/quartermaster/internal/note"
	"github.com/firefly-engineering/quartermaster/internal/send"
)

var md = goldmark.New()

// Sent statuses: an item in *Sent to other repos* records how far its ask has gone as one of
// them, in bold, after `→ <workspace label>,` (the worklog skill's *Other repos*).
const (
	statusToSend = "to send"
	statusSent   = "sent"
	statusAcked  = "acked"
	statusDone   = "done"
)

var sentStatuses = []string{statusToSend, statusSent, statusAcked, statusDone}

// line is what refresh reads from a work item's title before its summary.
type line struct {
	ref    note.Ref // the issue or PR its leading link names; the zero Ref for none
	status string   // its sent status, "" for none
	at     [2]int   // the status's bytes in the title
	label  string   // the workspace label after its `→`, "" for none
}

// parseLine reads a work item's title before its summary (note.SplitSummary) as markdown.
// Its ref is its leading link, and only when that link is to a GitHub issue or PR: a link
// further on is something it mentions, which proves nothing about it. Its sent status is its
// first bold run reading one of sentStatuses, and its label the word after the last `→`
// before that.
func parseLine(head string) line {
	src := []byte(head)
	para, ok := md.Parser().Parse(text.NewReader(src)).FirstChild().(*ast.Paragraph)
	if !ok {
		return line{}
	}
	var l line
	if link, ok := para.FirstChild().(*ast.Link); ok {
		l.ref, _ = githubRef(string(link.Destination))
	}
	var plain strings.Builder // the text before the status
	for c := para.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			plain.Write(c.Segment.Value(src))
		case *ast.Emphasis:
			t, ok := c.FirstChild().(*ast.Text)
			if c.Level != 2 || !ok || t.NextSibling() != nil || !slices.Contains(sentStatuses, string(t.Segment.Value(src))) {
				continue
			}
			l.status, l.at = string(t.Segment.Value(src)), [2]int{t.Segment.Start, t.Segment.Stop}
			if i := strings.LastIndex(plain.String(), "→"); i >= 0 {
				if f := strings.Fields(plain.String()[i+len("→"):]); len(f) > 0 {
					l.label = strings.TrimRight(f[0], ",")
				}
			}
			return l
		}
	}
	return l
}

// githubRef reads a GitHub issue or PR URL, https://github.com/<owner>/<repo>/(issues|pull)/<n>.
func githubRef(s string) (note.Ref, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" {
		return note.Ref{}, false
	}
	p := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(p) != 4 || p[2] != "issues" && p[2] != "pull" {
		return note.Ref{}, false
	}
	n, err := strconv.Atoi(p[3])
	if err != nil || strconv.Itoa(n) != p[3] {
		return note.Ref{}, false
	}
	ref, err := note.ParseRef(p[0]+"/"+p[1]+"#"+p[3], "")
	return ref, err == nil
}

// githubPrefix starts every link a reply is read for.
const githubPrefix = "https://github.com/"

// replyLinks finds the reply to item id's prompt in an agent's terminal text: whatever follows
// the line naming the item (send.PromptTag), which `quartermaster send` closes every prompt with. It
// returns the GitHub links in the reply; ok is false when the text holds no prompt for id.
func replyLinks(text, id string) (links []string, ok bool) {
	i := strings.LastIndex(text, send.PromptTag(id))
	if i < 0 {
		return nil, false
	}
	rest := text[i:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return nil, true
	}
	return githubLinks(rest[nl+1:]), true
}

// githubLinks are the GitHub URLs in text, in order and once each: each whitespace-separated
// word holding one, cut where markdown or prose closes it (a link's `)`, a code span's
// backtick, a sentence's full stop).
func githubLinks(text string) []string {
	var links []string
	for _, tok := range strings.Fields(text) {
		at := strings.Index(tok, githubPrefix)
		if at < 0 {
			continue
		}
		tok = tok[at:]
		if end := strings.IndexAny(tok, ")]>\"'`"); end >= 0 {
			tok = tok[:end]
		}
		tok = strings.TrimRight(tok, ".,;:!?")
		u, err := url.Parse(tok)
		if err != nil || u.Host != "github.com" || len(strings.Split(strings.Trim(u.Path, "/"), "/")) < 3 {
			continue
		}
		if !slices.Contains(links, tok) {
			links = append(links, tok)
		}
	}
	return links
}
