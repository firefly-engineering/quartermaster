package send

import (
	"slices"
	"strconv"
	"strings"

	"github.com/firefly-engineering/quartermaster/internal/rex"
)

// AgentName is the agent name for repo, the repo part of owner/repo, with suffix: the first of
// `<repo>-<suffix>`, then with `-2`, `-3`, … appended, that is not in taken, the names of the
// live agents (rex.Client.AgentList). A name is unique among the agents live when it is
// picked, so the counter absorbs two repos of one name, the owner left out. Two spawns at once
// can pick the same one: nothing finds an agent by its name, only by its pane (#174).
//
// Both parts are lowercased and every character outside [a-z0-9_-] becomes `-`; the repo part
// is then prefixed `a` unless it starts with a letter. Only the repo part is cut to fit 32 characters:
// the suffix and the counter never are. A fresh dispatch's suffix is the issue's number; a
// batch's, its item id; a session's, its harness. A suffix too long to leave the repo a character is not cut,
// so the name is longer than rex.MaxName.
func AgentName(repo, suffix string, taken []string) string {
	for k := 1; ; k++ {
		if name := agentName(repo, suffix, k); !slices.Contains(taken, name) {
			return name
		}
	}
}

// IsAgentName reports whether name is one AgentName gives repo and suffix, whatever the
// counter.
func IsAgentName(name, repo, suffix string) bool {
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
	if repo == "" || !rex.NameStart(rune(repo[0])) {
		repo = "a" + repo
	}
	t := "-" + sanitize(suffix)
	if k > 1 {
		t += "-" + strconv.Itoa(k)
	}
	return repo[:max(1, min(len(repo), rex.MaxName-len(t)))] + t
}

// sanitize lowercases s and maps every character not allowed in an agent name (rex.NameChar)
// to `-`.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if rex.NameChar(r) {
			return r
		}
		return '-'
	}, strings.ToLower(s))
}
