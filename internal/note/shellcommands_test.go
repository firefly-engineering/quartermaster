package note

import "testing"

func TestShellCommandID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, data, id string
		ok             bool
	}{
		{"worklog-send", `{"shell_commands":[
			{"id":"other","platform_specific_commands":{"default":"echo hi"}},
			{"id":"nf8lgvkx5s","platform_specific_commands":{"default":"PATH=\"/etc/profiles/per-user/$USER/bin:$PATH\" ~/.claude/skills/worklog/scripts/worklog-send {{vault_path}} {{_date}} {{_item}}"}}]}`,
			"nf8lgvkx5s", true},
		{"quartermaster send", `{"shell_commands":[{"id":"q1","platform_specific_commands":{"darwin":"/run/current-system/sw/bin/quartermaster send {{vault_path}} {{_date}} {{_item}}"}}]}`,
			"q1", true},
		{"quartermaster serve is not send", `{"shell_commands":[{"id":"q1","platform_specific_commands":{"default":"quartermaster serve"}}]}`, "", false},
		{"name inside a word", `{"shell_commands":[{"id":"q1","platform_specific_commands":{"default":"my-worklog-send-wrapper"}}]}`, "", false},
		{"no entry", `{"shell_commands":[]}`, "", false},
		{"no id", `{"shell_commands":[{"platform_specific_commands":{"default":"worklog-send"}}]}`, "", false},
		{"not json", `nope`, "", false},
		{"empty", ``, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			id, ok := ShellCommandID([]byte(c.data))
			if id != c.id || ok != c.ok {
				t.Errorf("got (%q, %v), want (%q, %v)", id, ok, c.id, c.ok)
			}
		})
	}
}
