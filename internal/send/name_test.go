package send

import (
	"strings"
	"testing"
)

func TestAgentName(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("abcdefghij", 4) // 40 characters
	for _, c := range []struct {
		name, repo, suffix string
		taken              []string
		want               string
	}{
		{"plain", "quartermaster", "97", nil, "quartermaster-97"},
		{"uppercase, dot and underscore", "My.Repo_X", "5", nil, "my-repo_x-5"},
		{"a leading digit", "1password", "5", nil, "a1password-5"},
		{"a leading dash", "-x", "5", nil, "a-x-5"},
		{"a harness suffix", "worklog", "claude", nil, "worklog-claude"},
		{"long, cut to fit", long, "123", nil, "abcdefghijabcdefghijabcdefgh-123"},
		{"long, its base taken", long, "123", []string{"abcdefghijabcdefghijabcdefgh-123"}, "abcdefghijabcdefghijabcdef-123-2"},
		{"base taken", "b", "5", []string{"b-5"}, "b-5-2"},
		{"base and base-2 taken", "b", "5", []string{"b-5", "b-5-2"}, "b-5-3"},
		{"another repo's name does not count", "b", "5", []string{"bb-5", "a-b-5", "c-5", "b-6"}, "b-5"},
	} {
		got := AgentName(c.repo, c.suffix, c.taken)
		if got != c.want {
			t.Errorf("%s: AgentName(%q, %q, %q) = %q, want %q", c.name, c.repo, c.suffix, c.taken, got, c.want)
		}
		if !herdrName(got) {
			t.Errorf("%s: %q is not a herdr agent name", c.name, got)
		}
		if !IsAgentName(got, c.repo, c.suffix) {
			t.Errorf("%s: IsAgentName(%q, %q, %q) is false", c.name, got, c.repo, c.suffix)
		}
	}
}

// herdrName reports whether s matches herdr's [a-z][a-z0-9_-]{0,31}.
func herdrName(s string) bool {
	if len(s) == 0 || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	return strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}
