// Command quartermaster keeps the day's worklog note and dispatches its work (docs/spec.md).
package main

import (
	"fmt"
	"io"
	"os"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "version" {
		if _, err := fmt.Fprintln(stdout, version); err != nil {
			return 1
		}
		return 0
	}
	_, _ = fmt.Fprintln(stderr, "usage: quartermaster version")
	return 2
}
