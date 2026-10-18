package note

import (
	"encoding/json"
	"maps"
	"path"
	"slices"
	"strings"
)

// ShellCommandID reads the Obsidian Shell commands plugin's settings, the bytes of
// `<vault>/.obsidian/plugins/obsidian-shellcommands/data.json`, and returns the id of the
// first shell command that runs `worklog-send` or `quartermaster send`: the id a ▶ button's
// URI executes. ok is false when the data is not JSON or holds no such command; the caller
// then renders no buttons and keeps the prompts.
func ShellCommandID(dataJSON []byte) (id string, ok bool) {
	var data struct {
		ShellCommands []struct {
			ID       string            `json:"id"`
			Commands map[string]string `json:"platform_specific_commands"`
		} `json:"shell_commands"`
	}
	if json.Unmarshal(dataJSON, &data) != nil {
		return "", false
	}
	for _, c := range data.ShellCommands {
		if c.ID == "" {
			continue
		}
		for _, platform := range slices.Sorted(maps.Keys(c.Commands)) {
			if runsSend(c.Commands[platform]) {
				return c.ID, true
			}
		}
	}
	return "", false
}

// runsSend reports whether a shell command line invokes `worklog-send`, or `quartermaster`
// with `send` as its subcommand, by any path. Words split on whitespace with no shell quoting,
// so a quoted path holding a space is not recognised.
func runsSend(cmd string) bool {
	words := strings.Fields(cmd)
	for i, w := range words {
		switch path.Base(w) {
		case "worklog-send":
			return true
		case "quartermaster":
			if i+1 < len(words) && words[i+1] == "send" {
				return true
			}
		}
	}
	return false
}
