package note

import "slices"

// Splicing edits a note's source by unit span (ADR 0002). Each function returns a new slice
// and leaves src alone; every byte outside the edited range is kept. Offsets and spans must
// come from Parse of the same src: they start lines, and a span ends one.

// Replace returns src with s's bytes replaced by unit.
func Replace(src []byte, s Span, unit []byte) []byte {
	return slices.Concat(src[:s.Start], unit, src[s.End:])
}

// Insert returns src with unit inserted at offset at, a line start. Use it for line units: a
// list item, a table row, a *Next* line.
func Insert(src []byte, at int, unit []byte) []byte {
	return slices.Concat(src[:at], unit, src[at:])
}

// InsertBlock inserts a block unit, as a ##### work item, at offset at, adding a blank line on
// each side where the neighbouring line is not blank already.
func InsertBlock(src []byte, at int, unit []byte) []byte {
	var before, after []byte
	if at > 0 && !blankBefore(src, at) {
		before = []byte("\n")
	}
	if at < len(src) && src[at] != '\n' {
		after = []byte("\n")
	}
	return slices.Concat(src[:at], before, unit, after, src[at:])
}

// Remove returns src without s. A block unit, one after a blank line, also takes the blank
// lines after it, so its neighbours stay one blank line apart; at the end of the note it
// takes the blank lines before it instead. A line unit inside a list or table takes nothing
// else. Removing a bucket's last entry leaves its heading bare; putting back `- —` is the
// caller's job.
func Remove(src []byte, s Span) []byte {
	start, end := s.Start, s.End
	if start == 0 || blankBefore(src, start) {
		for end < len(src) && src[end] == '\n' {
			end++
		}
		if end == len(src) {
			for start > 0 && blankBefore(src, start) {
				start--
			}
		}
	}
	return slices.Concat(src[:start], src[end:])
}

// blankBefore reports whether the line before offset at, a line start, is blank.
func blankBefore(src []byte, at int) bool {
	return at >= 2 && src[at-1] == '\n' && src[at-2] == '\n'
}
