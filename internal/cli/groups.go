package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func newGroupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "group", Short: "Manage session groups"}
	cmd.AddCommand(newGroupListCmd(), newGroupCreateCmd(), newGroupRenameCmd(), newGroupColorCmd(), newGroupPathCmd(), newGroupNestCmd(),
		newGroupDeleteCmd(), newGroupArchiveCmd(), newGroupUnarchiveCmd(), newGroupFindCmd())
	return cmd
}

func newGroupListCmd() *cobra.Command {
	var archived bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap snapshot) error {
				groups := []*ipc.Group{} // the daemon's, in order
				for _, g := range snap.Groups {
					if g.ArchivedAt.IsZero() || archived {
						for _, w := range snap.Snapshot.Groups {
							if w.ID == g.ID {
								groups = append(groups, w)
							}
						}
					}
				}
				if done, err := emitJSON(cmd, groups); done {
					return err
				}
				counts := map[string]int{}
				for _, s := range snap.Sessions {
					if s.Status != core.StatusArchived {
						counts[s.GroupID]++
					}
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tKIND\tGROUP\tCOLOR\tSESSIONS\tPATH\tSTATE")
				for _, g := range nestedOrder(snap.Groups) {
					if !g.ArchivedAt.IsZero() && !archived {
						continue
					}
					name := g.Name
					if g.ParentID != "" {
						name = "  └ " + name // inside the group above
					}
					state := ""
					if !g.ArchivedAt.IsZero() {
						state = "archived"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", g.ID, g.KindMark(), name, g.Color, counts[g.ID], tildeHome(g.Path), state)
				}
				return tw.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&archived, "archived", false, "include archived groups")
	return cmd
}

// nestedOrder is groups with each one's children right after it.
func nestedOrder(groups []*core.Group) []*core.Group {
	known := map[string]bool{}
	for _, g := range groups {
		known[g.ID] = true
	}
	var out []*core.Group
	for _, g := range groups {
		if g.ParentID != "" && known[g.ParentID] {
			continue
		}
		out = append(out, g)
		for _, c := range groups {
			if c.ParentID == g.ID {
				out = append(out, c)
			}
		}
	}
	return out
}

func newGroupNestCmd() *cobra.Command {
	var top bool
	cmd := &cobra.Command{
		Use:   "nest <group id> <parent id>|--top",
		Short: "Put a group inside another (two levels deep), or with --top back at the top",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if top == (len(args) == 2) {
				return usageError{errors.New("give the group it goes inside, or --top")}
			}
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				parent := ""
				if !top {
					p, err := resolveGroup(snap, args[1])
					if err != nil {
						return err
					}
					parent = p.ID
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupNest, GroupID: g.ID, ParentID: parent, Host: g.Host}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&top, "top", false, "put it back at the top")
	return cmd
}

func newGroupCreateCmd() *cobra.Command {
	var color, folder, parentID string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a group, or with --folder a folder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := folderPath(folder)
			if err != nil {
				return err
			}
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				parent := ""
				if parentID != "" {
					p, err := resolveGroup(snap, parentID)
					if err != nil {
						return err
					}
					parent = p.ID
				}
				var g ipc.Group
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutGroupCreate, Text: args[0], Color: color, Path: path, ParentID: parent}, &g); err != nil {
					return err
				}
				if g.Path != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Created folder %q (%s) at %s: %s\n", g.Name, g.Color, tildeHome(g.Path), g.ID)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Created group %q (%s): %s\n", g.Name, g.Color, g.ID)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&color, "color", "", "a theme color (blue, magenta, cyan, yellow, green, red, bright-<one>), a fixed one (orange, teal, pink…), or #RRGGBB (default: the next theme color)")
	cmd.Flags().StringVar(&folder, "folder", "", "make it a folder at this path, made if it's missing: new sessions in it start there")
	cmd.Flags().StringVar(&parentID, "parent", "", "make it inside this group, by id (two levels deep); it starts in the parent's color")
	return cmd
}

func newGroupPathCmd() *cobra.Command {
	var clearPath bool
	cmd := &cobra.Command{
		Use:   "path <group id> <path>|--clear",
		Short: "Make a group a folder at a path (made if it's missing), or with --clear a group again",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clearPath == (len(args) == 2) {
				return usageError{errors.New("give a path, or --clear")}
			}
			path := ""
			if !clearPath {
				var err error
				if path, err = folderPath(args[1]); err != nil {
					return err
				}
			}
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupPath, GroupID: g.ID, Path: path, Host: g.Host}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&clearPath, "clear", false, "clear the path: it's a group again")
	return cmd
}

// folderPath is a folder's path as the daemon takes it: absolute, from the
// current folder here; with --host, as given, for the host to resolve.
func folderPath(path string) (string, error) {
	if path == "" || targetHost != "" || filepath.IsAbs(path) || strings.HasPrefix(path, "~") {
		return path, nil
	}
	return filepath.Abs(path)
}

func groupMutation(use, short string, nargs int, build func(g *core.Group, args []string) ipc.Mutation) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(nargs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(build(g, args), nil)
			})
		},
	}
}

func newGroupRenameCmd() *cobra.Command {
	return groupMutation("rename <group id> <new name>", "Rename a group", 2, func(g *core.Group, a []string) ipc.Mutation {
		return ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g.ID, Text: a[1], Host: g.Host}
	})
}

func newGroupColorCmd() *cobra.Command {
	return groupMutation("color <group id> <color>", "Change a group's color", 2, func(g *core.Group, a []string) ipc.Mutation {
		return ipc.Mutation{Kind: ipc.MutGroupColor, GroupID: g.ID, Color: a[1], Host: g.Host}
	})
}

func newGroupDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <group id>",
		Short: "Delete a group, and archive its sessions (move out any to keep first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				n, running := groupSessions(snap, g)
				q := fmt.Sprintf("Delete group %q, and archive its %d session(s)? %d running session(s) will be stopped (conversations are kept).",
					g.Name, n, running)
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: g.ID, Host: g.Host}, nil)
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newGroupArchiveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "archive <group id>",
		Short: "Archive a group and all its sessions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				n, running := groupSessions(snap, g)
				q := fmt.Sprintf("Archive group %q and its %d session(s)? %d running session(s) will be stopped (conversations are kept).", g.Name, n, running)
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID, Host: g.Host}, nil)
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

// groupSessions counts g's live sessions, and the running ones among them.
func groupSessions(snap snapshot, g *core.Group) (n, running int) {
	for _, s := range snap.Sessions {
		if s.GroupID == g.ID && s.Host == g.Host && s.Status != core.StatusArchived {
			n++
			if !s.Stopped() {
				running++
			}
		}
	}
	return n, running
}

func newGroupUnarchiveCmd() *cobra.Command {
	var sessions bool
	var as string
	cmd := &cobra.Command{
		Use:   "unarchive <group id>",
		Short: "Bring an archived group back",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				g, err := resolveGroup(snap, args[0])
				if err != nil {
					return err
				}
				if g.ArchivedAt.IsZero() {
					return fmt.Errorf("%s isn't archived", g.Name)
				}
				err = c.mutate(ipc.Mutation{Kind: ipc.MutGroupUnarchive, GroupID: g.ID, Flag: sessions, Host: g.Host, Text: as}, nil)
				if as == "" && hasCode(err, ipc.CodeConflict) {
					for _, x := range snap.Groups {
						if x.Host == g.Host && x.ArchivedAt.IsZero() && strings.EqualFold(x.Name, g.Name) {
							return fmt.Errorf("can't unarchive %s: the group %s (%s) has that name. Unarchive it under a new one: acmd group unarchive %s --as %s",
								g.Name, x.Name, x.ID, shellQuote(g.ID), shellQuote(g.Name+" 2"))
						}
					}
				}
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&sessions, "sessions", false, "also unarchive the group's sessions (as paused)")
	cmd.Flags().StringVar(&as, "as", "", "bring it back under this name (when another group has taken its own)")
	return cmd
}
