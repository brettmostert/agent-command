package daemon

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// archivedCount is how many of the sessions with these Claude ids are archived.
func archivedCount(t *testing.T, c *ipc.Conn, cids ...string) int {
	t.Helper()
	snap, n := snapshot(t, c), 0
	for _, cid := range cids {
		if s := byClaude(snap, cid); s == nil {
			t.Fatalf("no session %s", cid)
		} else if s.Status == core.StatusArchived {
			n++
		}
	}
	return n
}

func groupArchived(t *testing.T, c *ipc.Conn, id string) bool {
	t.Helper()
	for _, g := range snapshot(t, c).Groups {
		if g.ID == id {
			return !g.ArchivedAt.IsZero()
		}
	}
	t.Fatalf("no group %s", id)
	return false
}

// Deleting a group archives its sessions; one moved out first is kept, in no
// group (PAN-187).
func TestDeletingAGroupArchivesItsSessions(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	ids := seedHosted(t, p, "a", "b", "keep")
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(ids...), GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(ids[2])}) // out of its group

	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: g.ID})
	snap := snapshot(t, c)
	if len(snap.Groups) != 0 {
		t.Errorf("the group is still there: %+v", snap.Groups)
	}
	if n := archivedCount(t, c, "a", "b"); n != 2 {
		t.Errorf("archived %d of the group's 2 sessions", n)
	}
	for _, cid := range []string{"a", "b", "keep"} {
		if s := byClaude(snap, cid); s.GroupID != "" {
			t.Errorf("%s is in %q, want no group", cid, s.GroupID)
		}
	}
	if archivedCount(t, c, "keep") != 0 {
		t.Error("a session moved out first was archived")
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: g.ID}, nil); !hasCode(err, ipc.CodeNotFound) {
		t.Errorf("deleting it again: %v", err)
	}
}

func TestArchivingAMissingGroupIsNotFound(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)

	err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: "nope"}, nil)
	if !hasCode(err, ipc.CodeNotFound) {
		t.Fatalf("want %s, got %v", ipc.CodeNotFound, err)
	}
}

func TestArchivingAGroupIsAllOrNothing(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	ids := seedHosted(t, p, "a", "b", "c")
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(ids...), GroupID: g.ID})

	// The group's own write fails, after every session's.
	db, err := sql.Open("sqlite", "file:"+p.DB()+"?_pragma=busy_timeout(5000)")
	must(t, err)
	defer db.Close()
	_, err = db.ExecContext(context.Background(), `CREATE TRIGGER fail_group_archive BEFORE UPDATE OF archived_at ON groups
		WHEN NEW.id = '`+g.ID+`' AND NEW.archived_at != 0 BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	must(t, err)

	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID}, nil); err == nil {
		t.Fatal("the archive should fail")
	}
	if n := archivedCount(t, c, "a", "b", "c"); n != 0 {
		t.Errorf("a failed archive left %d sessions archived", n)
	}
	if groupArchived(t, c, g.ID) {
		t.Error("a failed archive left the group archived")
	}

	// Once the failure is gone, the same archive works, and repeats safely.
	_, err = db.ExecContext(context.Background(), `DROP TRIGGER fail_group_archive`)
	must(t, err)
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID})
	if n := archivedCount(t, c, "a", "b", "c"); n != 3 {
		t.Errorf("archived %d of 3 sessions", n)
	}
	if !groupArchived(t, c, g.ID) {
		t.Error("the group isn't archived")
	}
}

func hasCode(err error, code string) bool {
	var re *ipc.RemoteError
	return errors.As(err, &re) && re.Code == code
}

func TestUnarchivingASessionBringsBackItsGroup(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	ids := seedHosted(t, p, "a", "b")
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(ids...), GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID})

	mutate(t, c, ipc.Mutation{Kind: ipc.MutUnarchive, Targets: ipc.Here(ids[0])})
	if groupArchived(t, c, g.ID) {
		t.Error("the session's group is still archived")
	}
	if n := archivedCount(t, c, "a", "b"); n != 1 {
		t.Errorf("want only b still archived, got %d archived", n)
	}
	if s := byClaude(snapshot(t, c), "a"); s.GroupID != g.ID {
		t.Errorf("a is in %s, not its group", s.GroupID)
	}
}

func TestUnarchivingASessionWhoseGroupsNameIsTaken(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	ids := seedHosted(t, p, "a")
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(ids...), GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "g"}) // the name, live again

	err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutUnarchive, Targets: ipc.Here(ids[0])}, nil)
	if !hasCode(err, ipc.CodeInvalid) || !strings.Contains(err.Error(), "another group has that name") {
		t.Fatalf("want invalid naming the clash, got %v", err)
	}
	if archivedCount(t, c, "a") != 1 || !groupArchived(t, c, g.ID) {
		t.Error("a failed unarchive changed the session or its group")
	}
}
