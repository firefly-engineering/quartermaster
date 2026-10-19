package dispatch

import (
	"slices"
	"strconv"
	"strings"
)

// maxName is the longest name herdr gives an agent: it takes [a-z][a-z0-9_-]{0,31}.
const maxName = 32

// AgentName is the herdr agent name for repo, the repo part of owner/repo, with suffix: the
// first of `<repo>-<suffix>`, then with `-2`, `-3`, … appended, that is not in taken, the
// names of the live agents (`herdr agent list`). herdr names are unique among all live agents,
// so the counter absorbs two repos of one name, the owner left out.
//
// Both parts are lowercased and every character outside [a-z0-9_-] becomes `-`; the repo part
// is then prefixed `a` unless it starts with a letter. Only the repo part is cut to fit 32 characters:
// the suffix and the counter never are. A fresh dispatch's suffix is the issue's number; a
// session's will be its harness. A suffix too long to leave the repo a character is not cut,
// so herdr refuses the name.
func AgentName(repo, suffix string, taken []string) string {
	repo = sanitize(repo)
	if repo == "" || repo[0] < 'a' || repo[0] > 'z' {
		repo = "a" + repo
	}
	tail := "-" + sanitize(suffix)
	for k := 1; ; k++ {
		t := tail
		if k > 1 {
			t += "-" + strconv.Itoa(k)
		}
		name := repo[:max(1, min(len(repo), maxName-len(t)))] + t
		if !slices.Contains(taken, name) {
			return name
		}
	}
}

// sanitize lowercases s and maps every character outside [a-z0-9_-] to `-`.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(s))
}
