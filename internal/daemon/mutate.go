package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// snapshot is everything the apps show. clientID, when set, says which
// client asks, for the warnings it dismissed.
func (d *Daemon) snapshot(ctx context.Context, clientID string) (ipc.Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sessions, err := d.store.Sessions(ctx, true)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	d.showBackground(sessions)
	groups, err := d.store.Groups(ctx, true)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	sets, err := d.store.Sets(ctx)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	queue, err := d.store.InboxQueue(ctx)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	jobs, err := d.jobs(ctx)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	var remote *ipc.RemoteStatus
	if d.remote != nil && !d.cfg.ClientOnly { // a client-only daemon has no Hosting
		st := d.remote.Status()
		remote = &st
	}
	wake := d.wakeState(ctx)
	dismissed, err := d.dismissed(ctx, clientID)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	// The known hosts' trees, as they have them.
	tree := d.client.Tree()
	return ipc.Snapshot{Sessions: append(ipc.SessionsOf(sessions), tree.Sessions...), Groups: append(ipc.GroupsOf(groups), tree.Groups...),
		Sets: append(wireSets(sets), tree.Sets...), Notices: d.notices(), Inbox: d.withHostInbox(queue),
		Jobs: jobs, Wake: wake, Remote: remote, RemoteHosts: d.remoteHosts(), Harnesses: d.harnesses(ctx).Harnesses,
		Settings: config.Values(d.config()), Health: d.health(sessions, jobs, wake, remote), Dismissed: dismissed,
		Features: d.features(), Update: d.update(), Speech: d.speechStatus()}, nil
}

// mutate applies one client change. It returns a value for group.create.
func (d *Daemon) mutate(ctx context.Context, m ipc.Mutation) (any, error) {
	if isBatch(m) {
		res, err := d.mutateMany(ctx, m)
		// One session that couldn't change is an error, with its code; for
		// several, each has its result.
		if err == nil && len(m.LocalIDs) == 1 && len(res.Results) == 1 && !res.Results[0].OK {
			r := res.Results[0]
			return nil, &codedError{code: r.Code, err: errors.New(r.Error)}
		}
		return res, err
	}
	if m.Kind == ipc.MutRemote { // takes d.mu itself: its subscribers run without it
		return nil, d.setRemote(ctx, m.LocalID, m.Flag)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.store
	if isSetMutation(m.Kind) {
		return d.mutateSet(ctx, st, m)
	}
	switch m.Kind {
	case ipc.MutSeen:
		return nil, d.updateSession(ctx, m.LocalID, func(s *core.Session) error {
			s.Unseen = false
			return nil
		})
	case ipc.MutRename:
		return nil, d.updateSession(ctx, m.LocalID, func(s *core.Session) error {
			name := strings.TrimSpace(m.Text)
			if name == "" {
				return invalid("name is empty")
			}
			s.Name, s.NameSource = name, core.NameUser
			return nil
		})
	case ipc.MutDescribe:
		return nil, d.updateSession(ctx, m.LocalID, func(s *core.Session) error {
			s.Description = strings.TrimSpace(m.Text)
			return nil
		})
	case ipc.MutArchive:
		return nil, d.archive(ctx, m.LocalID)
	case ipc.MutFinish:
		return nil, d.finish(ctx, m.LocalID)
	case ipc.MutUnarchive:
		return nil, d.unarchive(ctx, m.LocalID)
	case ipc.MutMove:
		return nil, st.MoveSessions(ctx, m.GroupID, m.LocalIDs...)
	case ipc.MutGroupCreate:
		return d.createGroup(ctx, m.Text, m.Color, m.Path, m.ParentID)
	case ipc.MutGroupNest:
		return nil, d.nestGroup(ctx, m.GroupID, m.ParentID)
	case ipc.MutGroupPath:
		return nil, d.setGroupPath(ctx, m.GroupID, m.Path)
	case ipc.MutGroupRename:
		return nil, st.RenameGroup(ctx, m.GroupID, m.Text)
	case ipc.MutGroupColor:
		color, err := groupColor(m.Color)
		if err != nil {
			return nil, err
		}
		if color == "" {
			return nil, invalid("a color is needed")
		}
		return nil, st.SetGroupColor(ctx, m.GroupID, color)
	case ipc.MutGroupDelete:
		return nil, d.deleteGroup(ctx, m.GroupID)
	case ipc.MutGroupArchive:
		return nil, d.archiveGroup(ctx, m.GroupID)
	case ipc.MutGroupUnarchive:
		return nil, d.unarchiveGroup(ctx, m.GroupID, m.Flag, m.Text)
	case ipc.MutPurge:
		if m.LocalID == "" {
			return nil, invalid("purge takes ids")
		}
		return nil, d.purge(ctx, m.LocalID)
	case ipc.MutGroupMove:
		return nil, d.moveGroup(ctx, m.GroupID, m.After, m.Before)
	case ipc.MutInboxRead:
		return nil, d.store.SetInboxRead(ctx, m.LocalItemIDs, m.Flag)
	case ipc.MutInboxDismiss:
		for _, id := range m.LocalItemIDs {
			if err := d.store.HandleInboxItem(ctx, id, core.InboxDismissed); err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown mutation %q", m.Kind)
	}
}

func (d *Daemon) updateSession(ctx context.Context, id string, fn func(*core.Session) error) error {
	s, err := d.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return d.store.UpdateSession(ctx, s)
}

// finish stops a session's host (its agent keeps the conversation) and marks
// it finished; views showing it go back to their list. Showing it again
// resumes it.
func (d *Daemon) finish(ctx context.Context, id string) error {
	s, err := d.store.Session(ctx, id)
	if err != nil {
		return err
	}
	switch s.Status {
	case core.StatusArchived:
		return errors.New("that session is archived")
	case core.StatusFinished:
		return nil
	}
	d.stopHost(s)
	core.Apply(s, core.Event{Kind: core.EventFinished, Time: d.eventTime(s)})
	if err := d.store.CloseInboxItems(ctx, s.ID); err != nil {
		return err
	}
	if err := d.store.UpdateSession(ctx, s); err != nil {
		return err
	}
	return d.store.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID, ClaudeSessionID: s.ClaudeSessionID,
		Kind: string(core.EventFinished), Detail: string(s.Status)})
}

// archive stops a session's host (its agent keeps the conversation) and
// marks it archived.
func (d *Daemon) archive(ctx context.Context, id string) error {
	s, err := d.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if s.Status == core.StatusArchived {
		return nil
	}
	d.stopHost(s)
	return d.recordArchived(ctx, d.store, s)
}

// recordArchived archives s in st: its status, its Inbox items and its
// event. Stopping its process is the caller's job.
func (d *Daemon) recordArchived(ctx context.Context, st *store.Store, s *core.Session) error {
	core.Apply(s, core.Event{Kind: core.EventArchived, Time: d.eventTime(s)})
	if err := st.CloseInboxItems(ctx, s.ID); err != nil {
		return err
	}
	if err := st.UpdateSession(ctx, s); err != nil {
		return err
	}
	return st.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID, ClaudeSessionID: s.ClaudeSessionID,
		Kind: string(core.EventArchived), Detail: string(s.Status)})
}

// unarchive brings a session back, and its group with it if the group is
// archived, so it never lands in a group no one sees.
func (d *Daemon) unarchive(ctx context.Context, id string) error {
	return d.store.WithTx(ctx, func(st *store.Store) error {
		s, err := st.Session(ctx, id)
		if err != nil {
			return err
		}
		if s.IsTerminal() {
			return invalid("an archived terminal's shell has ended: start a new one")
		}
		core.Apply(s, core.Event{Kind: core.EventUnarchived, Time: d.eventTime(s)})
		if err := st.UpdateSession(ctx, s); err != nil {
			return err
		}
		if s.GroupID == "" {
			return nil // in no group
		}
		return bringBack(ctx, st, s.GroupID)
	})
}

// bringBack unarchives group id if it's archived, and its parent if that is:
// what's unarchived inside them never lands where no one sees it.
func bringBack(ctx context.Context, st *store.Store, id string) error {
	for id != "" {
		g, err := st.Group(ctx, id)
		if err != nil {
			return err
		}
		if !g.ArchivedAt.IsZero() {
			err = st.SetGroupArchived(ctx, g.ID, false)
			if errors.Is(err, store.ErrDuplicateName) {
				return &codedError{code: ipc.CodeConflict, err: fmt.Errorf("the group %q can't come back: another group has that name. "+
					"Unarchive it under a new name, or rename the other one first", g.Name)}
			}
			if err != nil {
				return err
			}
		}
		id = g.ParentID
	}
	return nil
}

// family is group id and the groups inside it.
func family(ctx context.Context, st *store.Store, id string) ([]string, error) {
	groups, err := st.Groups(ctx, true)
	if err != nil {
		return nil, err
	}
	ids := []string{id}
	for _, g := range groups {
		if g.ParentID == id {
			ids = append(ids, g.ID)
		}
	}
	return ids, nil
}

// unarchiveGroup brings back a group and the groups inside it, and its
// parent if that's archived; with sessions, the sessions in all of them too,
// as paused. All or nothing.
//
// With a name, it comes back under that name: for when another group has
// taken its own.
func (d *Daemon) unarchiveGroup(ctx context.Context, id string, sessions bool, name string) error {
	return d.store.WithTx(ctx, func(st *store.Store) error {
		ids, err := family(ctx, st, id)
		if err != nil {
			return err
		}
		if _, err := st.Group(ctx, id); err != nil {
			return err
		}
		if strings.TrimSpace(name) != "" {
			if err := st.RenameGroup(ctx, id, name); err != nil {
				return err
			}
		}
		for _, gid := range ids {
			if err := bringBack(ctx, st, gid); err != nil {
				return err
			}
		}
		if !sessions {
			return nil
		}
		all, err := st.Sessions(ctx, true)
		if err != nil {
			return err
		}
		for _, s := range all {
			if slices.Contains(ids, s.GroupID) && s.Status == core.StatusArchived && !s.IsTerminal() {
				core.Apply(s, core.Event{Kind: core.EventUnarchived, Time: d.eventTime(s)})
				if err := st.UpdateSession(ctx, s); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// nestGroup puts a group inside parent, after its other children, or
// ("") at the top, after the other top-level groups. Groups nest two levels:
// the parent must be at the top, and a group with children stays there.
func (d *Daemon) nestGroup(ctx context.Context, id, parent string) error {
	return d.store.WithTx(ctx, func(st *store.Store) error {
		g, err := st.Group(ctx, id)
		if err != nil {
			return err
		}
		if parent != "" {
			if err := checkParent(ctx, st, parent); err != nil {
				return err
			}
			if parent == id {
				return invalid("a group can't go inside itself")
			}
			ids, err := family(ctx, st, id)
			if err != nil {
				return err
			}
			if len(ids) > 1 {
				return invalid("%q has groups inside it, so it can't go inside another: groups nest two levels", g.Name)
			}
		}
		if g.ParentID == parent {
			return nil
		}
		return st.SetGroupParent(ctx, id, parent, lastKey(ctx, st, parent)+10)
	})
}

// checkParent says whether group id can hold groups: it's there, live, and
// at the top.
func checkParent(ctx context.Context, st *store.Store, id string) error {
	p, err := st.Group(ctx, id)
	if err != nil {
		return err
	}
	if !p.ArchivedAt.IsZero() {
		return invalid("%q is archived", p.Name)
	}
	if p.ParentID != "" {
		return invalid("%q is inside another group, so it can't hold groups: groups nest two levels", p.Name)
	}
	return nil
}

// lastKey is the largest sort key among parent's children ("" the top).
func lastKey(ctx context.Context, st *store.Store, parent string) int {
	groups, _ := st.Groups(ctx, true)
	last := 0
	for _, g := range groups {
		if g.ParentID == parent && g.SortKey > last {
			last = g.SortKey
		}
	}
	return last
}

// createGroup makes a group, or with a path a folder: the path is made if
// it's missing, before anything is saved.
// Inside a parent, it starts in the parent's color unless one is given.
func (d *Daemon) createGroup(ctx context.Context, name, color, path, parent string) (*core.Group, error) {
	color, err := groupColor(color)
	if err != nil {
		return nil, err
	}
	if parent != "" {
		if err := checkParent(ctx, d.store, parent); err != nil {
			return nil, err
		}
		if p, err := d.store.Group(ctx, parent); err == nil && color == "" {
			color = p.Color
		}
	}
	if path != "" {
		if path, err = makeFolder(path); err != nil {
			return nil, err
		}
	}
	var g *core.Group
	err = d.store.WithTx(ctx, func(st *store.Store) error {
		var err error
		if g, err = st.CreateGroup(ctx, name, color); err != nil {
			return err
		}
		if parent != "" {
			g.ParentID, g.SortKey = parent, lastKey(ctx, st, parent)+10
			if err := st.SetGroupParent(ctx, g.ID, parent, g.SortKey); err != nil {
				return err
			}
		}
		if path == "" {
			return nil
		}
		g.Path = path
		return st.SetGroupPath(ctx, g.ID, path)
	})
	return g, err
}

// setGroupPath makes a group a folder with path, made if it's missing, or
// ("") a group again. Its sessions don't move.
func (d *Daemon) setGroupPath(ctx context.Context, id, path string) error {
	if _, err := d.store.Group(ctx, id); err != nil {
		return err
	}
	if path != "" {
		var err error
		if path, err = makeFolder(path); err != nil {
			return err
		}
	}
	return d.store.SetGroupPath(ctx, id, path)
}

// folderPath is the path of the folder with id; "" for a group, no group or
// one that's gone.
func (d *Daemon) folderPath(ctx context.Context, id string) string {
	if id == "" {
		return ""
	}
	g, err := d.store.Group(ctx, id)
	if err != nil {
		return ""
	}
	return g.Path
}

// makeFolder makes a folder's path, and its parents, if they're missing,
// and returns it clean. It must be absolute, or start with ~/, and not be a
// file.
func makeFolder(path string) (string, error) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(path) {
		return "", invalid("a folder's path must be absolute, not %q", path)
	}
	path = filepath.Clean(path)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return "", invalid("%s is a file, not a folder", path)
	}
	if err := os.MkdirAll(path, 0o755); err != nil { // #nosec G301 -- a folder for the user's own work
		return "", invalid("couldn't make %s: %v", path, err)
	}
	return path, nil
}

// archiveGroup archives a group, the groups inside it and all their
// sessions, all or nothing.
func (d *Daemon) archiveGroup(ctx context.Context, id string) error {
	return d.withGroupArchived(ctx, id, func(st *store.Store, ids []string) error {
		for _, gid := range ids {
			if err := st.SetGroupArchived(ctx, gid, true); err != nil {
				return err
			}
		}
		return nil
	})
}

// deleteGroup deletes a group and the groups inside it, and archives all
// their sessions, all or nothing: to keep one, move it out first.
func (d *Daemon) deleteGroup(ctx context.Context, id string) error {
	return d.withGroupArchived(ctx, id, func(st *store.Store, ids []string) error {
		for i := len(ids) - 1; i >= 0; i-- { // the children, then the group
			if err := st.DeleteGroup(ctx, ids[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// withGroupArchived archives the live sessions in group id and the groups
// inside it, then does then with the groups' ids (the group first), in one
// transaction. It checks the group before it changes anything, and stops
// the sessions' processes only once it's saved.
func (d *Daemon) withGroupArchived(ctx context.Context, id string, then func(*store.Store, []string) error) error {
	if _, err := d.store.Group(ctx, id); err != nil {
		return err
	}
	var archived []*core.Session
	err := d.store.WithTx(ctx, func(st *store.Store) error {
		ids, err := family(ctx, st, id)
		if err != nil {
			return err
		}
		sessions, err := st.Sessions(ctx, false)
		if err != nil {
			return err
		}
		for _, s := range sessions {
			if !slices.Contains(ids, s.GroupID) || s.Status == core.StatusArchived {
				continue
			}
			if err := d.recordArchived(ctx, st, s); err != nil {
				return err
			}
			archived = append(archived, s)
		}
		return then(st, ids)
	})
	if err != nil {
		return err
	}
	for _, s := range archived {
		d.stopHost(s)
	}
	return nil
}

// moveGroup puts a group right after the group after, or right before the
// group before (section 7.3 of the design: reorders name a neighbor).
func (d *Daemon) moveGroup(ctx context.Context, id, after, before string) error {
	if (after == "") == (before == "") {
		return errors.New("group.move needs the group it goes after, or before")
	}
	groups, err := d.store.Groups(ctx, false)
	if err != nil {
		return err
	}
	// Groups move among their siblings: the same parent, or the top.
	parent := ""
	for _, g := range groups {
		if g.ID == id {
			parent = g.ParentID
		}
	}
	order := slices.DeleteFunc(slices.Clone(groups), func(g *core.Group) bool { return g.ParentID != parent })
	sort.SliceStable(order, func(i, j int) bool { return order[i].SortKey < order[j].SortKey })
	at := -1
	for i, g := range order {
		if g.ID == id {
			at = i
		}
	}
	if at < 0 {
		return errors.New("that group can't be moved")
	}
	moving := order[at]
	order = append(order[:at:at], order[at+1:]...)
	next := slices.IndexFunc(order, func(g *core.Group) bool { return g.ID == after || g.ID == before })
	if next < 0 {
		return fmt.Errorf("group %s isn't one to move next to", after+before)
	}
	if after != "" {
		next++
	}
	order = slices.Insert(order, next, moving)
	for i, g := range order {
		if g.SortKey != i+1 {
			if err := d.store.SetGroupSortKey(ctx, g.ID, i+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// groupColor checks a group color from a client ("" is none given).
func groupColor(c string) (string, error) {
	if strings.TrimSpace(c) == "" {
		return "", nil
	}
	color, err := core.NormalizeColor(c)
	if err != nil {
		return "", invalid("%v", err)
	}
	return color, nil
}
