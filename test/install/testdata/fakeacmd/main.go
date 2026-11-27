// Command fakeacmd stands in for acmd in the install scripts' tests. It's
// built with -ldflags "-X main.version=<version>".
//
//	fakeacmd version  prints "agent-command <version> (fake)"
//	fakeacmd <args>   appends "<version> <args> argv0=<argv0> exe=<exe>"
//	                  to $FAKEACMD_LOG
//
// It's in testdata, so ./... and the coverage profile leave it out.
package main

import (
	"fmt"
	"os"
	"strings"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "version" {
		fmt.Printf("agent-command %s (fake)\n", version)
		return
	}
	exe, _ := os.Executable()
	f, err := os.OpenFile(os.Getenv("FAKEACMD_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeacmd:", err)
		os.Exit(1)
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s argv0=%s exe=%s\n", version, strings.Join(args, " "), os.Args[0], exe)
}
