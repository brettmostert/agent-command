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
		newGroupDeleteCmd(), newGroupArchiveCmd(), newGroupUnarchiveCmd(),
		newGroupCollapseCmd("collapse", true), newGroupCollapseCmd("expand", false))
	return cmd
}

func newGroupListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List groups",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(_ *client, snap snapshot) error {
				groups := []*ipc.Group{} // the daemon's, in order
				for _, g := range snap.Groups {
					if g.ArchivedAt.IsZero() || all {
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
				fmt.Fprintln(tw, "KIND\tGROUP\tCOLOR\tSESSIONS\tPATH\tSTATE")
				for _, g := range nestedOrder(snap.Groups) {
					if !g.ArchivedAt.IsZero() && !all {
						continue
					}
					name := g.Name
					if g.ParentID != "" {
						name = "  └ " + name // inside the group above
					}
					state := ""
					switch {
					case !g.ArchivedAt.IsZero():
						state = "archived"
					case snap.ViewState.IsCollapsed(g.ID):
						state = "collapsed"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", g.KindMark(), name, g.Color, counts[g.ID], tildeHome(g.Path), state)
				}
				return tw.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include archived groups")
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
		Use:   "nest <group> <parent>|--top",
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
	var color, folder, parentName string
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
				if parentName != "" {
					p, err := resolveGroup(snap, parentName)
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
					fmt.Fprintf(cmd.OutOrStdout(), "Created folder %q (%s) at %s\n", g.Name, g.Color, tildeHome(g.Path))
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Created group %q (%s)\n", g.Name, g.Color)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&color, "color", "", "blue, magenta, cyan, yellow, green or red (default: next in the palette)")
	cmd.Flags().StringVar(&folder, "folder", "", "make it a folder at this path, made if it's missing: new sessions in it start there")
	cmd.Flags().StringVar(&parentName, "parent", "", "make it inside this group (two levels deep); it starts in the parent's color")
	return cmd
}

func newGroupPathCmd() *cobra.Command {
	var clearPath bool
	cmd := &cobra.Command{
		Use:   "path <group> <path>|--clear",
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
	return groupMutation("rename <group> <new name>", "Rename a group", 2, func(g *core.Group, a []string) ipc.Mutation {
		return ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g.ID, Text: a[1], Host: g.Host}
	})
}

func newGroupColorCmd() *cobra.Command {
	return groupMutation("color <group> <color>", "Change a group's color", 2, func(g *core.Group, a []string) ipc.Mutation {
		return ipc.Mutation{Kind: ipc.MutGroupColor, GroupID: g.ID, Color: a[1], Host: g.Host}
	})
}

func newGroupDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <group>",
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
		Use:   "archive <group>",
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
	cmd := &cobra.Command{
		Use:   "unarchive <group>",
		Short: "Bring an archived group back",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				var g *core.Group
				for _, x := range snap.Groups {
					if !x.ArchivedAt.IsZero() && (x.ID == args[0] || strings.EqualFold(x.Name, args[0])) {
						g = x
					}
				}
				if g == nil {
					return fmt.Errorf("no archived group named %q", args[0])
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupUnarchive, GroupID: g.ID, Flag: sessions, Host: g.Host}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&sessions, "sessions", false, "also unarchive the group's sessions (as paused)")
	return cmd
}

func newGroupCollapseCmd(use string, collapsed bool) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   use + " <group>|--all",
		Short: map[bool]string{true: "Collapse a group (or all)", false: "Expand a group (or all)"}[collapsed],
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return errors.New("give a group name or --all")
			}
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				id := ""
				if !all {
					g, err := resolveGroup(snap, args[0])
					if err != nil {
						return err
					}
					id = g.ID
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: id, Flag: collapsed}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "all groups")
	return cmd
}

func newViewModeCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "view-mode groups|list",
		Short:     "Switch acmd list between group view and list view",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"groups", "list"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, _ snapshot) error {
				return c.mutate(ipc.Mutation{Kind: ipc.MutViewMode, Text: args[0]}, nil)
			})
		},
	}
}

func newSortCmd() *cobra.Command {
	var desc bool
	cmd := &cobra.Command{
		Use:       "sort created|name|activity|status|cwd",
		Short:     "Set the sort order for acmd list",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"created", "name", "activity", "status", "cwd"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, _ snapshot) error {
				return c.mutate(ipc.Mutation{Kind: ipc.MutViewSort, Text: args[0], Flag: desc}, nil)
			})
		},
	}
	cmd.Flags().BoolVar(&desc, "desc", false, "descending")
	return cmd
}
