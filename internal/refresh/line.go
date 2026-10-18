package refresh

import (
	"net/url"
	"slices"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/note"
	"github.com/firefly-engineering/quartermaster/internal/send"
)

// The sent statuses an item in *Sent to other repos* goes through, as internal/note reads and
// writes them.
const (
	statusToSend = note.SentToSend
	statusSent   = note.SentSent
	statusAcked  = note.SentAcked
	statusDone   = note.SentDone
)

// line is what refresh reads from a work item's title before its summary.
type line struct {
	ref    note.Ref // the issue or PR its leading link names; the zero Ref for none
	status string   // its sent status, "" for none
	label  string   // the workspace label after its `→`, "" for none
}

// parseLine reads work item it as internal/note parsed it: its ref, the GitHub issue or PR its
// title's leading link names (a link further on is something it mentions, which proves nothing
// about it), and its sent status and label (note.Sent).
func parseLine(it *note.Item) line {
	var l line
	l.ref, _ = note.ParseRef(it.Ref, "")
	if it.Sent != nil {
		l.status, l.label = it.Sent.Status, it.Sent.To
	}
	return l
}

// githubPrefix starts every link a reply is read for.
const githubPrefix = "https://github.com/"

// replyLinks finds the reply to item id's prompt in an agent's terminal text: whatever follows
// the line naming the item (send.PromptTag), which `worklog send` closes every prompt with, up
// to the next prompt sent to the pane (the next prompt tag for any item). It returns
// the GitHub links in the reply; ok is false when the text holds no prompt for id.
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
	reply := rest[nl+1:]
	return githubLinks(reply[:nextPromptTag(reply)]), true
}

// nextPromptTag is where the first prompt tag for any item (send.PromptTag) starts in text, or
// len(text) for none. A bare mention of an item, not followed by " in ", is not a tag.
func nextPromptTag(text string) int {
	marker := send.Item("")
	for at := 0; ; {
		i := strings.Index(text[at:], marker)
		if i < 0 {
			return len(text)
		}
		at += i
		tail := text[at+len(marker):]
		if sp := strings.IndexByte(tail, ' '); sp > 0 && strings.HasPrefix(text[at:], send.PromptTag(tail[:sp])) {
			return at
		}
		at += len(marker)
	}
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
