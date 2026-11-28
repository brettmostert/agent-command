package cli

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// now is swappable in tests.
var now = time.Now

// listSorts are acmd list's sort keys.
var listSorts = []string{"created", "name", "activity", "status", "cwd"}

func newListCmd() *cobra.Command {
	var vs core.ViewState
	var lf listFilters
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the sessions the app runs, with their ids",
		Long: "Lists the sessions, this machine's and your Remote hosts', with the id commands take. How it's\n" +
			"laid out is the command's own: the flags, each time. The daemon keeps no view.\n\n" +
			"Each filter is a flag, or --filter key=value; --filter with no key is a text search. Different\n" +
			"filters must all match, and one given more than once matches any of its values. The keys:\n" +
			"  on         local, remote (any Remote host), or a host's name\n" +
			"  resumable  true: what can be resumed, here and on the hosts, with the agents' own sessions\n" +
			"             acmd doesn't have (no acmd id: acmd resume <agent id> resumes one)\n" +
			"  harness    a harness's name, e.g. Work or Terminal\n" +
			"  group      a group's id\n" +
			"  status     a status, e.g. busy or waiting\n" +
			"  archived   true: include archived sessions\n" +
			"  text       text in the name, description, folder or group (g:<text> for the group only)",
		Example: "  acmd list --on studio --resumable --harness Work\n" +
			"  acmd list --filter on=studio --filter resumable=true --filter harness=Work   # the same\n" +
			"  acmd list --filter api --on remote",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if vs.Mode != "groups" && vs.Mode != "list" {
				return usageError{fmt.Errorf("--mode is groups or list, not %q", vs.Mode)}
			}
			if !slices.Contains(listSorts, vs.Sort) {
				return usageError{fmt.Errorf("--sort is %s, not %q", strings.Join(listSorts, ", "), vs.Sort)}
			}
			f, text, err := lf.parse()
			if err != nil {
				return usageError{err}
			}
			vs.ShowArchived = f.Archived
			vs.Filter = text
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				var groups []*core.Group
				for _, id := range f.Group {
					g, err := resolveGroupAnywhere(snap, id)
					if err != nil {
						return err
					}
					groups = append(groups, g)
				}
				f.Group = nil // matched by host and id below
				if f.Resumable {
					return listResumable(cmd, c, f, groups, text)
				}
				if err := knownHosts(snap, f.On); err != nil {
					return err
				}
				var rows []listRow
				for _, r := range listRows(snap, vs) {
					if f.Match(r.Session) && inGroups(r.Session, groups) {
						rows = append(rows, r)
					}
				}
				if jsonOut {
					return writeListJSON(jsonWriter(cmd), snap, rows)
				}
				return writeListTable(cmd.OutOrStdout(), rows)
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&vs.Mode, "mode", "groups", "groups, or list: one list, not in groups")
	fl.StringVar(&vs.Sort, "sort", "created", strings.Join(listSorts, ", "))
	fl.BoolVar(&vs.Desc, "desc", false, "sort descending")
	fl.StringArrayVar(&lf.filter, "filter", nil, "a filter as key=value, or text to search for (repeat it)")
	fl.StringArrayVar(&lf.on, "on", nil, "only sessions here (local), on any Remote host (remote), or on this host")
	fl.BoolVar(&lf.resumable, "resumable", false, "what can be resumed, with the agents' own sessions acmd doesn't have")
	fl.StringArrayVar(&lf.harness, "harness", nil, "only sessions in this harness, by name")
	fl.StringArrayVar(&lf.group, "group", nil, "only sessions in this group, by id")
	fl.StringArrayVar(&lf.status, "status", nil, "only these statuses (comma-separated, e.g. busy,waiting)")
	fl.BoolVar(&lf.archived, "archived", false, "include archived sessions and groups")
	return cmd
}

// listFilters are acmd list's filters as given: each flag, and --filter's
// key=value form of the same.
type listFilters struct {
	filter, on, harness, group, status []string
	resumable, archived                bool
}

// parse is the filters as session.list takes them, and the text to search
// for. A --filter whose key isn't one of the filters is text.
func (lf listFilters) parse() (ipc.SessionFilter, string, error) {
	f := ipc.SessionFilter{On: lf.on, Resumable: lf.resumable, Harness: lf.harness, Group: lf.group, Archived: lf.archived}
	add := func(list *[]string, v string) {
		for _, x := range strings.Split(v, ",") {
			if x = strings.TrimSpace(x); x != "" {
				*list = append(*list, x)
			}
		}
	}
	for _, v := range lf.status {
		add(&f.Status, v)
	}
	var text []string
	for _, v := range lf.filter {
		key, value, _ := strings.Cut(v, "=")
		switch key {
		case "on":
			f.On = append(f.On, value)
		case "harness":
			f.Harness = append(f.Harness, value)
		case "group":
			f.Group = append(f.Group, value)
		case "status":
			add(&f.Status, value)
		case "text":
			text = append(text, value)
		case "resumable", "archived":
			on, err := strconv.ParseBool(value)
			if err != nil {
				return f, "", fmt.Errorf("--filter %s is true or false, not %q", key, value)
			}
			if key == "resumable" {
				f.Resumable = f.Resumable || on
			} else {
				f.Archived = f.Archived || on
			}
		default:
			text = append(text, v)
		}
	}
	return f, strings.Join(text, " "), nil
}

// knownHosts checks that each host on names is a known host.
func knownHosts(snap snapshot, on []string) error {
	for _, name := range on {
		if name == ipc.OnLocal || name == ipc.OnRemote {
			continue
		}
		if !slices.ContainsFunc(snap.RemoteHosts, func(h ipc.RemoteHost) bool { return h.Name == name }) {
			return &ExitError{Code: exitNotFound, Err: fmt.Errorf("there's no Remote host named %q (acmd remote list shows them)", name)}
		}
	}
	return nil
}

func inGroups(s *core.Session, groups []*core.Group) bool {
	return len(groups) == 0 || slices.ContainsFunc(groups, func(g *core.Group) bool { return s.GroupID == g.ID && s.Host == g.Host })
}

// listResumable is list --resumable: what session.list says can be resumed,
// here and on the hosts, the most recently active first.
func listResumable(cmd *cobra.Command, c *client, f ipc.SessionFilter, groups []*core.Group, text string) error {
	rows, unreached, err := resumableSessions(c, f)
	if err != nil {
		return err
	}
	terms := strings.Fields(strings.ToLower(text))
	rows = slices.DeleteFunc(rows, func(s *ipc.Session) bool {
		hay := strings.ToLower(strings.Join([]string{s.Name, s.ID, s.ClaudeSessionID, s.Cwd}, "\x00"))
		return !inGroups(s.Core(), groups) || slices.ContainsFunc(terms, func(t string) bool { return !strings.Contains(hay, t) })
	})
	for _, host := range unreached {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s didn't answer: its sessions aren't listed.\n", host)
	}
	if jsonOut {
		enc := json.NewEncoder(jsonWriter(cmd))
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	w := cmd.OutOrStdout()
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "Nothing to resume.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tAGENT ID\tSTATUS\tNAME\tHARNESS\tDIR\tWHERE\tLAST")
	for _, w := range rows {
		s := w.Core()
		id, status := cmp.Or(s.ID, "-"), cmp.Or(string(s.Status), "-")
		if w.AgentLive {
			status += " (live)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", id, cmp.Or(s.ClaudeSessionID, "-"), status, display(s.Name, 40),
			w.HarnessName, filepath.Base(s.Cwd), where(s), age(s))
	}
	return tw.Flush()
}

// resumableSessions is every page of session.list for f, with resumable.
func resumableSessions(c *client, f ipc.SessionFilter) ([]*ipc.Session, []string, error) {
	f.Resumable = true
	req := ipc.SessionListRequest{Filter: f, Page: ipc.Page{Limit: ipc.PageMax}}
	var rows []*ipc.Session
	for {
		var page ipc.SessionPage
		if err := c.call(ipc.OpSessionList, req, &page); err != nil {
			return nil, nil, err
		}
		rows = append(rows, page.Items...)
		if page.NextCursor == "" || len(page.Items) == 0 {
			return rows, page.Unreached, nil
		}
		req.Cursor = page.NextCursor
	}
}

type listRow struct {
	Session *core.Session
	Group   *core.Group
}

// listRows lays the sessions out in vs, as the sidebar does, every group open.
func listRows(snap snapshot, vs core.ViewState) []listRow {
	var out []listRow
	for _, r := range core.Layout(snap.Sessions, snap.Groups, vs) {
		if r.Session != nil {
			out = append(out, listRow{Session: r.Session, Group: r.Group})
		}
	}
	return out
}

// resolveGroupAnywhere finds a group by id on this machine or a host.
func resolveGroupAnywhere(snap snapshot, id string) (*core.Group, error) {
	for _, g := range snap.Groups {
		if g.ID == strings.TrimSpace(id) {
			return g, nil
		}
	}
	return resolveGroup(snap, id)
}

func writeListTable(w io.Writer, rows []listRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "No sessions.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tGROUP\tNAME\tHARNESS\tDIR\tWHERE\tLAST")
	for _, r := range rows {
		status := string(r.Session.Status)
		if r.Session.Unseen {
			status += "•"
		}
		group := "-" // in no group
		if r.Group != nil {
			group = r.Group.Name
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Session.ID, status, group, display(r.Session.Name, 40),
			harnessOf(r.Session), filepath.Base(r.Session.Cwd), where(r.Session), age(r.Session))
	}
	return tw.Flush()
}

// writeListJSON is list --json: the rows' sessions as the daemon sent them
// (internal/ipc.Session), in the list's order.
func writeListJSON(w io.Writer, snap snapshot, rows []listRow) error {
	byID := map[string]*ipc.Session{}
	for _, s := range snap.Snapshot.Sessions {
		byID[s.ID] = s
	}
	out := make([]*ipc.Session, 0, len(rows))
	for _, r := range rows {
		if s := byID[r.Session.ID]; s != nil {
			out = append(out, s)
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// harnessOf is the name of what a session runs in.
func harnessOf(s *core.Session) string { return cmp.Or(s.HarnessName, core.KindTitle(s.HarnessKind)) }

// where says where a session runs: "↗ <host>" on a Remote host, else "acmd".
func where(s *core.Session) string {
	if s.OnHost() {
		if s.HostGone {
			return "↗ " + s.Host + " (gone)"
		}
		return "↗ " + s.Host
	}
	return "acmd"
}

func age(s *core.Session) string {
	t := s.LastEventAt
	if t.IsZero() {
		t = s.CreatedAt
	}
	if t.IsZero() {
		return "-"
	}
	d := now().Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// display shortens a name for the table.
func display(s string, limit int) string {
	if s == "" {
		return "(unnamed)"
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "…"
}

func newRenameCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:     "rename <id>… --to <name> | rename <session> <new name>",
		Short:   "Rename sessions (several are numbered: \"api - 1\", \"api - 2\")",
		Example: "  acmd rename <id> api\n  acmd rename <id> <id> <id> --to api   # api - 1, api - 2, api - 3",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, name := args, to
			if to == "" { // the old way: one session, then its name
				if len(args) < 2 {
					return errors.New("give the new name: acmd rename <session> <name>, or several sessions with --to <name>")
				}
				sel, name = args[:1], strings.Join(args[1:], " ")
			}
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				ids, err := resolveSessions(snap, sel)
				if err != nil {
					return err
				}
				var res ipc.Results
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutRename, Targets: ids, Text: name}, &res); err != nil {
					return err
				}
				return reportResults(cmd.OutOrStdout(), res, "")
			})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "the new name, for one or more sessions")
	return cmd
}

// resolveSessions finds each selector's session, once each, in order.
func resolveSessions(snap snapshot, sels []string) ([]ipc.Target, error) {
	var ids []ipc.Target
	for _, sel := range sels {
		s, err := resolveSession(snap, sel)
		if err != nil {
			return nil, err
		}
		if t := targetOf(s); !slices.Contains(ids, t) {
			ids = append(ids, t)
		}
	}
	return ids, nil
}

// targetOf names s on its host, for an op.
func targetOf(s *core.Session) ipc.Target { return ipc.Target{Host: s.Host, ID: s.ID} }

// reportResults says what didn't work, one line each, and fails if any
// didn't. done, when set, says how many did.
func reportResults(w io.Writer, res ipc.Results, done string) error {
	ok := len(res.Results) - res.Failed()
	for _, r := range res.Results {
		if !r.OK {
			fmt.Fprintf(w, "%s: %s (%s)\n", r.ID, r.Error, r.Code)
		}
	}
	if done != "" {
		fmt.Fprintf(w, done, ok)
	}
	if n := res.Failed(); n > 0 {
		return &ExitError{Code: exitPartial, Err: fmt.Errorf("%d of %d didn't change", n, len(res.Results))}
	}
	return nil
}

func newDescribeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "describe <id> <text>",
		Short: "Set a session's description (empty text clears it)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutDescribe, Targets: []ipc.Target{targetOf(s)}, Text: strings.Join(args[1:], " ")}, nil)
			})
		},
	}
}

func newArchiveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "archive <id>…",
		Short: "Archive sessions (ends them; an agent's conversation is kept)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				ids, err := resolveSessions(snap, args)
				if err != nil {
					return err
				}
				q := fmt.Sprintf("Archive %d sessions?", len(ids))
				if len(ids) == 1 {
					s, _ := resolveSession(snap, args[0])
					q = fmt.Sprintf("Archive %q?", s.Name)
					if !s.Stopped() {
						q = fmt.Sprintf("Archive %q? It will be stopped (the conversation is kept).", s.Name)
					}
				}
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				var res ipc.Results
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutArchive, Targets: ids}, &res); err != nil {
					return err
				}
				return reportResults(cmd.OutOrStdout(), res, "")
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newFinishCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "finish <id>",
		Short: "Finish a session: its agent stops; it stays listed and showing it resumes it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				q := fmt.Sprintf("Finish %s? %s stops; the conversation is kept and showing it resumes it.", nameOf(s), core.AgentName(s.HarnessKind))
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutFinish, Targets: []ipc.Target{targetOf(s)}}, nil); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Finished %s.\n", nameOf(s))
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

func newUnarchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unarchive <id>",
		Short: "Bring an archived session back (as paused)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutUnarchive, Targets: []ipc.Target{targetOf(s)}}, nil)
			})
		},
	}
}

func newMoveCmd() *cobra.Command {
	var noGroup bool
	var newGroup string
	cmd := &cobra.Command{
		Use:   "move <id>... <group id> | move <id>... --new-group <name> | move <id>... --no-group",
		Short: "Move sessions to a group, or out of their group",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if noGroup && newGroup != "" {
				return usageError{errors.New("give --new-group or --no-group, not both")}
			}
			ownGroup := !noGroup && newGroup == "" // the last argument is the group's id
			if ownGroup && len(args) < 2 {
				return usageError{errors.New("give the sessions and a group's id, --new-group <name> or --no-group")}
			}
			return withClient(cmd.Context(), func(c *client, snap snapshot) error {
				sel, groupID := args, ""
				if ownGroup {
					sel, groupID = args[:len(args)-1], args[len(args)-1]
				}
				ids, err := resolveSessions(snap, sel)
				if err != nil {
					return err
				}
				// The group is on the sessions' host: a session stays on its own.
				host := ids[0].Host
				for _, t := range ids {
					if t.Host != host {
						return usageError{fmt.Errorf("those sessions are on different hosts: move the sessions of one host at a time")}
					}
				}
				if noGroup {
					return c.mutate(ipc.Mutation{Kind: ipc.MutMove, Targets: ids, Host: host}, nil)
				}
				g, err := groupFor(c, snap, groupID, newGroup, host)
				if err != nil {
					return err
				}
				return c.mutate(ipc.Mutation{Kind: ipc.MutMove, Targets: ids, GroupID: g.ID, Host: host}, nil)
			})
		},
	}
	cmd.Flags().StringVar(&newGroup, "new-group", "", "make a group with this name, and move them into it")
	cmd.Flags().BoolVar(&noGroup, "no-group", false, "take the sessions out of their group")
	return cmd
}

// createGroupOn makes a group on host ("" for this machine).
func createGroupOn(c *client, name, color, host string) (*core.Group, error) {
	var g ipc.Group
	if err := c.mutate(ipc.Mutation{Kind: ipc.MutGroupCreate, Text: name, Color: color, Host: host}, &g); err != nil {
		return nil, err
	}
	g.Host = host
	return g.Core(), nil
}

// groupFor is the group sessions go into on host: the live one with id, or
// with newName a new one of that name. A name never picks a group.
func groupFor(c *client, snap snapshot, id, newName, host string) (*core.Group, error) {
	if newName != "" {
		return createGroupOn(c, newName, "", host)
	}
	return liveGroupOn(snap, id, host)
}
