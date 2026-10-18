package note

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// The sent statuses (spec §6): how far the ask of an item in *Sent to other repos* has gone.
const (
	SentToSend = "to send"
	SentSent   = "sent"
	SentAcked  = "acked"
	SentDone   = "done"
)

// SentStatuses are the sent statuses, in the order an ask goes through them.
var SentStatuses = []string{SentToSend, SentSent, SentAcked, SentDone}

// sentArrow leads the workspace label an item in *Sent to other repos* is sent to.
const sentArrow = "→"

// Sent is the sent status of an item in *Sent to other repos*, read from its title before its
// summary: `<title> → <to>, **<status>**`, then `, <note>` when it has one.
type Sent struct {
	Title  string // the title before ` → `: its ref link, text, cost and route as written
	To     string // the herdr workspace label the ask goes to
	Status string // one of SentStatuses
	Note   string // the text after the status and its `, `, as when it was asked and the reply's links; "" for none
}

// SentUnit is a sent status as the renderer writes it after a work item's title (and, in the
// list form, its cost and route): ` → <to>, **<status>**`, then `, <note>` when it has one.
type SentUnit struct {
	To     string // the herdr workspace label
	Status string // one of SentStatuses
	Note   string // "" for none
}

// sent reads the sent status of it, a work item in *Sent to other repos*.
func (p *parser) sent(it *Item) {
	if p.part != nil || p.bucket == nil || p.bucket.Name != SentBucket {
		return
	}
	head, _ := SplitSummary(it.Title)
	it.Sent = parseSent(head)
}

// parseSent reads head, a work item's title before its summary, as markdown. Its sent status is
// the first bold run reading one of SentStatuses after a plain-text `→` and `<to>, `; nil when
// it has none in that form.
func parseSent(head string) *Sent {
	src := []byte(head)
	para, ok := md.Parser().Parse(text.NewReader(src)).FirstChild().(*ast.Paragraph)
	if !ok {
		return nil
	}
	arrow := -1 // the offset of the last `→` in plain text so far
	for c := para.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *ast.Text:
			if i := bytes.LastIndex(c.Segment.Value(src), []byte(sentArrow)); i >= 0 {
				arrow = c.Segment.Start + i
			}
		case *ast.Emphasis:
			t, ok := c.FirstChild().(*ast.Text)
			if c.Level != 2 || !ok || t.NextSibling() != nil || arrow < 0 {
				continue
			}
			status := string(t.Segment.Value(src))
			open, end := t.Segment.Start-2, t.Segment.Stop+2 // the `**` around it
			if !slices.Contains(SentStatuses, status) || open < 0 || end > len(src) {
				continue
			}
			to, spaced := strings.CutPrefix(head[arrow+len(sentArrow):open], " ")
			if to, ok = strings.CutSuffix(to, ", "); !spaced || !ok || !validSentTo(to) {
				return nil
			}
			s := &Sent{Title: strings.TrimSpace(head[:arrow]), To: to, Status: status}
			if rest := head[end:]; rest != "" {
				if s.Note, ok = strings.CutPrefix(rest, ", "); !ok {
					return nil
				}
				s.Note = strings.TrimSpace(s.Note)
			}
			if s.Title == "" {
				return nil
			}
			return s
		}
	}
	return nil
}

// validSentTo reports whether to is a workspace label the sent form can hold: one word, with
// no `,` or `*`.
func validSentTo(to string) bool {
	return to != "" && !strings.ContainsFunc(to, isSpace) && !strings.ContainsAny(to, ",*")
}

// sentTail renders u as it follows a title: ` → <to>, **<status>**[, <note>]`.
func sentTail(u SentUnit) (string, error) {
	if !slices.Contains(SentStatuses, u.Status) {
		return "", fmt.Errorf("sent status %q: want one of %s", u.Status, strings.Join(SentStatuses, ", "))
	}
	if !validSentTo(u.To) {
		return "", fmt.Errorf("sent to %q: want a workspace label, one word with no , or *", u.To)
	}
	tail := " " + sentArrow + " " + u.To + ", **" + u.Status + "**"
	if note := strings.TrimSpace(u.Note); note != "" {
		tail += ", " + note
	}
	return tail, nil
}
