package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Commands take ids, never names: a name, or a number in a list, can mean
// another session later. find turns a name into the ids to give them.

// foundSession is a session as acmd find --json has it.
type foundSession struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Host   string      `json:"host,omitempty"`
	Group  string      `json:"group,omitempty"` // the group's id
	Status core.Status `json:"status"`
}

// foundGroup is a group as acmd group find --json has it.
type foundGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Path     string `json:"path,omitempty"`
	Archived bool   `json:"archived,omitempty"`
}

func newFindCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "find <name>",
		Short: "Print the ids of the sessions whose name has this in it",
		Long: "Prints the id of each session whose name has <name> in it, ignoring case: this machine's and\n" +
			"your Remote hosts'. Commands take ids. With one match it exits 0; with none, or more than one,\n" +
			"it exits 1 (with all of them printed).",
		Example: "  acmd attach $(acmd find api)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(_ *client, snap snapshot) error {
				matches := core.Find(snap.Sessions, args[0])
				ids := make([]string, len(matches))
				found := make([]foundSession, len(matches))
				for i, s := range matches {
					ids[i] = s.ID
					found[i] = foundSession{ID: s.ID, Name: s.Name, Host: s.Host, Group: s.GroupID, Status: s.Status}
				}
				return printFound(cmd, ids, found, "session", args[0])
			})
		},
	}
}

func newGroupFindCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "find <name>",
		Short: "Print the ids of the groups whose name has this in it",
		Long: "Prints the id of each group whose name has <name> in it, ignoring case, archived ones included:\n" +
			"this machine's and your Remote hosts'. Group commands take ids. With one match it exits 0; with\n" +
			"none, or more than one, it exits 1 (with all of them printed).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(_ *client, snap snapshot) error {
				text := strings.ToLower(strings.TrimSpace(args[0]))
				var ids []string
				var found []foundGroup
				for _, g := range snap.Groups {
					if text != "" && strings.Contains(strings.ToLower(g.Name), text) {
						ids = append(ids, g.ID)
						found = append(found, foundGroup{ID: g.ID, Name: g.Name, Host: g.Host, Parent: g.ParentID, Path: g.Path,
							Archived: !g.ArchivedAt.IsZero()})
					}
				}
				if found == nil {
					found = []foundGroup{} // [] in JSON, not null
				}
				return printFound(cmd, ids, found, "group", args[0])
			})
		},
	}
}

// printFound prints what find found: the ids one per line, or with --json
// found. Anything but one match fails, after printing them all.
func printFound[T any](cmd *cobra.Command, ids []string, found []T, what, name string) error {
	if done, err := emitJSON(cmd, found); done && err != nil {
		return err
	} else if !done {
		for _, id := range ids {
			fmt.Fprintln(cmd.OutOrStdout(), id)
		}
	}
	switch len(ids) {
	case 0:
		return fmt.Errorf("no %s's name has %q in it", what, name)
	case 1:
		return nil
	}
	return fmt.Errorf("%d %ss match: give one id", len(ids), what)
}

// completeSessions completes a session's id, each described by its name.
func completeSessions(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	snap, ok := completionSnapshot(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, s := range snap.Sessions {
		if s.Status != core.StatusArchived {
			out = append(out, s.ID+"\t"+describeSession(s))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeGroups completes the first argument with one of this machine's
// groups' ids, each described by its name.
func completeGroups(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	snap, ok := completionSnapshot(cmd)
	if !ok || len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, g := range snap.Groups {
		if g.Host == "" {
			out = append(out, g.ID+"\t"+g.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// describeSession is a session's name, and its host, for completion.
func describeSession(s *core.Session) string {
	d := cmpOr(s.Name, "(unnamed)")
	if s.Host != "" {
		d += " on " + s.Host
	}
	return d
}

// completionSnapshot is the running daemon's snapshot. Completion never
// starts the daemon, and gives up quickly.
func completionSnapshot(cmd *cobra.Command) (snapshot, bool) {
	conn, done, err := runningDaemon(cmd.Context())
	if err != nil {
		return snapshot{}, false
	}
	defer done()
	var wire ipc.Snapshot
	if conn.Call(ipc.OpSnapshot, nil, &wire) != nil {
		return snapshot{}, false
	}
	return snapshotOf(wire), true
}

// completeIDs has the commands that take ids complete them, each with its
// name beside it.
func completeIDs(root *cobra.Command) {
	groupFlag := func(c *cobra.Command, flag string) {
		_ = c.RegisterFlagCompletionFunc(flag, func(c *cobra.Command, _ []string, s string) ([]string, cobra.ShellCompDirective) {
			return completeGroups(c, nil, s)
		})
	}
	for _, c := range root.Commands() {
		switch c.Name() {
		case "attach", "resume", "open", "screen", "rename", "describe", "archive", "finish", "unarchive", "move":
			c.ValidArgsFunction = completeSessions
		case "list", "new":
			groupFlag(c, "group")
		case "group":
			for _, g := range c.Commands() {
				switch g.Name() {
				case "nest", "path", "rename", "color", "delete", "archive", "unarchive":
					g.ValidArgsFunction = completeGroups
				case "create":
					groupFlag(g, "parent")
				}
			}
		case "remote":
			for _, r := range c.Commands() {
				if r.Name() == "on" || r.Name() == "off" {
					r.ValidArgsFunction = completeSessions
				}
			}
		}
	}
}
