package dispatch

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// wsPrefix starts the name of a fresh dispatch's workspace for issue N: issue-<N>. A batch's
// workspace is named after its item id, which never starts with it.
const wsPrefix = "issue-"

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
// batch's, its item id; a session's will be its harness. A suffix too long to leave the repo a character is not cut,
// so herdr refuses the name.
func AgentName(repo, suffix string, taken []string) string {
	for k := 1; ; k++ {
		if name := agentName(repo, suffix, k); !slices.Contains(taken, name) {
			return name
		}
	}
}

// IsFreshAgent reports whether name is one a fresh dispatch gives the agent it starts in
// workspace ws, whatever its counter: `<repo>-<N>` in `<workspace_root>/<owner>/<repo>/issue-<N>`,
// or a batch's `<repo>-<item-id>` in `<workspace_root>/<owner>/<repo>/<item-id>`. An agent
// renamed since, or a session's, is not.
func IsFreshAgent(name, ws string) bool {
	suffix := strings.TrimPrefix(filepath.Base(ws), wsPrefix)
	return isAgentName(name, filepath.Base(filepath.Dir(ws)), suffix)
}

// isAgentName reports whether name is one AgentName gives repo and suffix, whatever the
// counter.
func isAgentName(name, repo, suffix string) bool {
	if name == agentName(repo, suffix, 1) {
		return true
	}
	i := strings.LastIndexByte(name, '-')
	k, err := strconv.Atoi(name[i+1:])
	return i >= 0 && err == nil && k > 1 && name == agentName(repo, suffix, k)
}

// agentName is AgentName's k-th candidate: `<repo>-<suffix>`, then with `-<k>` for k > 1.
func agentName(repo, suffix string, k int) string {
	repo = sanitize(repo)
	if repo == "" || repo[0] < 'a' || repo[0] > 'z' {
		repo = "a" + repo
	}
	t := "-" + sanitize(suffix)
	if k > 1 {
		t += "-" + strconv.Itoa(k)
	}
	return repo[:max(1, min(len(repo), maxName-len(t)))] + t
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
