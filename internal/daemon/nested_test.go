package daemon

import (
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func groupByID(t *testing.T, c *ipc.Conn, id string) *ipc.Group {
	t.Helper()
	for _, g := range snapshot(t, c).Groups {
		if g.ID == id {
			return g
		}
	}
	return nil
}

func newGroup(t *testing.T, c *ipc.Conn, m ipc.Mutation) ipc.Group {
	t.Helper()
	m.Kind = ipc.MutGroupCreate
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, m, &g))
	return g
}

// Groups nest two levels (PAN-189): a child starts in its parent's color,
// and nesting refuses a third level, a cycle and an archived parent.
func TestGroupsNestTwoLevels(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	parent := newGroup(t, c, ipc.Mutation{Text: "Backend", Color: "green"})
	child := newGroup(t, c, ipc.Mutation{Text: "Workers", ParentID: parent.ID})
	if child.Parent != parent.ID || child.Color != "green" {
		t.Errorf("a new child: parent %q, color %q", child.Parent, child.Color)
	}
	own := newGroup(t, c, ipc.Mutation{Text: "Bugs", ParentID: parent.ID, Color: "red"})
	if own.Color != "red" {
		t.Error("a color given wins over the parent's")
	}
	other := newGroup(t, c, ipc.Mutation{Text: "Docs"})

	// Into a parent, and back to the top.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupNest, GroupID: other.ID, ParentID: parent.ID})
	if g := groupByID(t, c, other.ID); g.Parent != parent.ID || g.Color == "green" {
		t.Errorf("nested: %+v (an existing group keeps its color)", g)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupNest, GroupID: other.ID})
	if groupByID(t, c, other.ID).Parent != "" {
		t.Error("back to the top")
	}

	for name, m := range map[string]ipc.Mutation{
		"a third level":            {GroupID: other.ID, ParentID: child.ID},
		"a parent into a child":    {GroupID: parent.ID, ParentID: other.ID},
		"into itself":              {GroupID: other.ID, ParentID: other.ID},
		"create inside a child":    {Kind: ipc.MutGroupCreate, Text: "deep", ParentID: child.ID},
		"into a group that's gone": {GroupID: other.ID, ParentID: "nope"},
	} {
		if m.Kind == "" {
			m.Kind = ipc.MutGroupNest
		}
		err := c.Call(ipc.OpMutate, m, nil)
		if !hasCode(err, ipc.CodeInvalid) && !hasCode(err, ipc.CodeNotFound) {
			t.Errorf("%s: want refused, got %v", name, err)
		}
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: other.ID})
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupNest, GroupID: own.ID, ParentID: other.ID}, nil); !hasCode(err, ipc.CodeInvalid) {
		t.Errorf("into an archived group: %v", err)
	}

	// Reordering stays among siblings: Workers and Bugs swap, Backend stays.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: own.ID, Before: child.ID})
	if a, b := groupByID(t, c, own.ID), groupByID(t, c, child.ID); a.Order >= b.Order || a.Parent != parent.ID {
		t.Errorf("reorder among siblings: %d, %d", a.Order, b.Order)
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: own.ID, Before: parent.ID}, nil); err == nil {
		t.Error("a child reordered among the top-level groups")
	}
}

// Archive and delete take everything under a group; unarchiving a child or
// one of its sessions brings its parent back.
func TestAGroupsFamilyArchivesAndDeletesTogether(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	ids := seedHosted(t, p, "top", "inner", "loose")
	parent := newGroup(t, c, ipc.Mutation{Text: "Backend"})
	child := newGroup(t, c, ipc.Mutation{Text: "Workers", ParentID: parent.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(ids[0]), GroupID: parent.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(ids[1]), GroupID: child.ID})

	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: parent.ID})
	if !groupArchived(t, c, parent.ID) || !groupArchived(t, c, child.ID) || archivedCount(t, c, "top", "inner") != 2 {
		t.Fatal("archiving a parent should take its child and every session under it")
	}
	if archivedCount(t, c, "loose") != 0 {
		t.Error("a session outside the family was archived")
	}

	// A session in the child comes back with its group and the parent.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutUnarchive, Targets: ipc.Here(ids[1])})
	if groupArchived(t, c, child.ID) || groupArchived(t, c, parent.ID) {
		t.Error("unarchiving a session should bring back its group and its parent")
	}
	if archivedCount(t, c, "top") != 1 {
		t.Error("only the session asked for comes back")
	}

	// Archiving the child leaves the parent; unarchiving the parent with its
	// sessions brings back everything under it.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: child.ID})
	if !groupArchived(t, c, child.ID) || groupArchived(t, c, parent.ID) {
		t.Error("archiving a child should leave its parent")
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: parent.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupUnarchive, GroupID: parent.ID, Flag: true})
	if groupArchived(t, c, child.ID) || archivedCount(t, c, "top", "inner") != 0 {
		t.Error("unarchiving a parent with its sessions should bring back everything under it")
	}
	// Unarchiving just the child brings its parent back too.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: parent.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupUnarchive, GroupID: child.ID})
	if groupArchived(t, c, parent.ID) || groupArchived(t, c, child.ID) {
		t.Error("unarchiving a child should bring back its parent")
	}

	// Deleting the parent deletes the child and archives every session under them.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutUnarchive, Targets: ipc.Here(ids[0], ids[1])})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: parent.ID})
	if groupByID(t, c, parent.ID) != nil || groupByID(t, c, child.ID) != nil {
		t.Error("the family should be gone")
	}
	snap := snapshot(t, c)
	for _, cid := range []string{"top", "inner"} {
		if s := byClaude(snap, cid); s.Status != core.StatusArchived || s.GroupID != "" {
			t.Errorf("%s after the delete: %s in %q", cid, s.Status, s.GroupID)
		}
	}
}
