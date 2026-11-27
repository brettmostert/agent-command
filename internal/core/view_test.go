package core

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func fixtureView() ([]*Session, []*Group) {
	proj := &Group{ID: "g1", Name: "PROJ-123 auth", SortKey: 10, CreatedAt: at(1)}
	bill := &Group{ID: "g2", Name: "billing", SortKey: 20, CreatedAt: at(2)}
	ss := []*Session{
		{ID: "a", Name: "refactor middleware", GroupID: "g1", Cwd: "/w/api-server", Status: StatusBusy, CreatedAt: at(1), LastEventAt: at(50)},
		{ID: "b", Name: "fix flaky e2e", GroupID: "g1", Cwd: "/w/web", Status: StatusWaiting, CreatedAt: at(2), LastEventAt: at(10)},
		{ID: "c", Name: "docs: hooks", Cwd: "/w/cli-tool", Status: StatusDone, CreatedAt: at(3), LastEventAt: at(30)},
		{ID: "d", Name: "invoice export", GroupID: "g2", Cwd: "/w/billing", Status: StatusDone, Unseen: true, CreatedAt: at(4), LastEventAt: at(40)},
		{ID: "e", Name: "old spike", Cwd: "/w/scratch", Status: StatusArchived, CreatedAt: at(5)},
	}
	return ss, []*Group{bill, proj} // c and e are in no group
}

// render turns rows into a compact string: headers as [Name n urgent], sessions as #N:id.
func render(rows []Row) string {
	var parts []string
	for _, r := range rows {
		if r.IsHeader() {
			h := "[" + r.Title()
			if r.Collapsed {
				h += " +"
			}
			parts = append(parts, h+"]")
			continue
		}
		if r.Number == 0 {
			parts = append(parts, "-:"+r.Session.ID)
		} else {
			parts = append(parts, itoa(r.Number)+":"+r.Session.ID)
		}
	}
	return strings.Join(parts, " ")
}

func itoa(n int) string { return string(rune('0' + n)) }

func TestLayoutListView(t *testing.T) {
	ss, gs := fixtureView()
	tests := []struct {
		vs   ViewState
		want string
	}{
		{ViewState{Mode: "list", Sort: "created"}, "1:a 2:b 3:c 4:d"},
		{ViewState{Mode: "list", Sort: "created", Desc: true}, "1:d 2:c 3:b 4:a"},
		{ViewState{Mode: "list", Sort: "name"}, "1:c 2:b 3:d 4:a"},
		{ViewState{Mode: "list", Sort: "activity", Desc: true}, "1:a 2:d 3:c 4:b"},
		{ViewState{Mode: "list", Sort: "status"}, "1:b 2:a 3:d 4:c"}, // waiting, busy, unseen done, done
		{ViewState{Mode: "list", Sort: "cwd"}, "1:a 2:d 3:c 4:b"},
		{ViewState{Mode: "list", ShowArchived: true}, "1:a 2:b 3:c 4:d 5:e"},
		{ViewState{Mode: "list", Filter: "FLAKY"}, "1:b"},
		{ViewState{Mode: "list", Filter: "g:proj"}, "1:a 2:b"},
		{ViewState{Mode: "list", Filter: "g:proj web"}, "1:b"}, // AND
		{ViewState{Mode: "list", Filter: "billing"}, "1:d"},    // matches group name and cwd
		{ViewState{Mode: "list", Filter: "nothing-matches"}, ""},
		{ViewState{Mode: "list", Filter: "g: hooks"}, "1:c"}, // empty g: ignored
		{ViewState{Mode: "list", Filter: "g:no"}, "1:c"},     // sessions in no group
	}
	for _, tt := range tests {
		if got := render(Layout(ss, gs, tt.vs)); got != tt.want {
			t.Errorf("%+v:\n got %s\nwant %s", tt.vs, got, tt.want)
		}
	}
}

func TestLayoutGroupView(t *testing.T) {
	ss, gs := fixtureView()
	rows := Layout(ss, gs, ViewState{Mode: "groups", Sort: "created"})
	if got := render(rows); got != "[PROJ-123 auth] 1:a 2:b [billing] 3:d [No group] 4:c" {
		t.Errorf("group view: %s", got)
	}
	h := rows[0]
	if !h.IsHeader() || h.Count != 2 || h.Urgent != StatusWaiting {
		t.Errorf("header: %+v", h)
	}

	// Collapsed group: header shows count + most urgent status, sessions get no number.
	gs[1].Collapsed = true
	rows = Layout(ss, gs, ViewState{Mode: "groups"})
	if got := render(rows); got != "[PROJ-123 auth +] [billing] 1:d [No group] 2:c" {
		t.Errorf("collapsed: %s", got)
	}
	if rows[0].Urgent != StatusWaiting || rows[0].Count != 2 {
		t.Errorf("collapsed header must show the waiting session: %+v", rows[0])
	}

	// Filtering expands matching groups and hides the rest.
	if got := render(Layout(ss, gs, ViewState{Mode: "groups", Filter: "flaky"})); got != "[PROJ-123 auth] 1:b" {
		t.Errorf("filtered: %s", got)
	}
	gs[1].Collapsed = false

	// Empty groups show without a filter; "No group" shows only with sessions in it.
	empty := &Group{ID: "g3", Name: "empty", SortKey: 30}
	if got := render(Layout(ss, append(gs, empty), ViewState{Mode: "groups"})); !strings.Contains(got, "[empty] [No group]") {
		t.Errorf("empty group hidden: %s", got)
	}
	if got := render(Layout(ss[:2], gs, ViewState{Mode: "groups"})); strings.Contains(got, "No group") {
		t.Errorf("an empty No group shows: %s", got)
	}

	// A "No group" header isn't a group, and never collapses.
	rows = Layout(ss, gs, ViewState{Mode: "groups", Collapsed: []string{""}})
	if last := rows[len(rows)-2]; last.Group != nil || last.Collapsed || last.Title() != NoGroupTitle || last.Count != 1 {
		t.Errorf("No group header: %+v", last)
	}

	// Archived groups are hidden; their sessions show in no group.
	arch := &Group{ID: "g2", Name: "billing", ArchivedAt: at(9)}
	if got := render(Layout(ss, []*Group{arch, gs[1]}, ViewState{Mode: "groups"})); got != "[PROJ-123 auth] 1:a 2:b [No group] 3:c 4:d" {
		t.Errorf("archived group: %s", got)
	}
}

func TestLayoutWithoutGroupsHasNoHeaders(t *testing.T) {
	ss, _ := fixtureView()
	for _, s := range ss {
		s.GroupID = ""
	}
	if got := render(Layout(ss, nil, ViewState{Mode: "groups"})); got != "1:a 2:b 3:c 4:d" {
		t.Errorf("no groups: %s", got)
	}
	// An unknown group id is no group.
	ss[0].GroupID = "gone"
	if got := render(Layout(ss[:1], nil, ViewState{Mode: "groups"})); got != "1:a" {
		t.Errorf("unknown group: %s", got)
	}
}

func TestNaturalCompare(t *testing.T) {
	ordered := []string{"", "a", "proj-2", "proj-9", "proj-10", "proj-010x", "proj-100", "proj-a", "é"}
	for i := 0; i < len(ordered)-1; i++ {
		if naturalCompare(ordered[i], ordered[i+1]) >= 0 {
			t.Errorf("%q should sort before %q", ordered[i], ordered[i+1])
		}
		if naturalCompare(ordered[i+1], ordered[i]) <= 0 {
			t.Errorf("%q should sort after %q", ordered[i+1], ordered[i])
		}
	}
	if naturalCompare("x7", "x07") != 0 || naturalCompare("same", "same") != 0 {
		t.Error("equal values should compare 0")
	}
	big := strings.Repeat("9", 40)
	if naturalCompare("n"+big, "n1"+big) >= 0 {
		t.Error("long digit runs must compare by magnitude without overflow")
	}
}

func TestResolve(t *testing.T) {
	ss, gs := fixtureView()
	ss[0].ClaudeSessionID = "9df2c810-full"
	rows := Layout(ss, gs, ViewState{Mode: "list"})
	cases := map[string]string{"2": "b", "a": "a", "9df2c810-full": "a", "FIX": "b", "invoice": "d"}
	for sel, want := range cases {
		s, err := Resolve(rows, ss, sel)
		if err != nil || s.ID != want {
			t.Errorf("Resolve(%q) = %v, %v; want %s", sel, s, err, want)
		}
	}
	for sel, wantErr := range map[string]error{"": ErrNoMatch, "9": ErrNoMatch, "zzz": ErrNoMatch} {
		if _, err := Resolve(rows, ss, sel); !errors.Is(err, wantErr) {
			t.Errorf("Resolve(%q) err = %v", sel, err)
		}
	}
	ss = append(ss, &Session{ID: "f", Name: "fix the docs", CreatedAt: time.Now()})
	if _, err := Resolve(rows, ss, "fix"); !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), "fix the docs") {
		t.Errorf("ambiguous: %v", err)
	}
}

func TestNextAttention(t *testing.T) {
	at := func(n int) time.Time { return time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC) }
	mk := func(id string, st Status, unseen bool, since int) *Session {
		return &Session{ID: id, Status: st, Unseen: unseen, StatusSince: at(since)}
	}
	// Created in this order, but waiting since different times.
	done, seen, errS := mk("done", StatusDone, true, 1), mk("seen", StatusDone, false, 2), mk("err", StatusError, false, 3)
	newer, older := mk("wait-new", StatusWaiting, false, 9), mk("wait-old", StatusWaiting, false, 5)
	archived := mk("gone", StatusArchived, false, 0)
	ss := []*Session{done, seen, errS, newer, older, archived}
	for shown, want := range map[string]string{
		"":         "wait-old", // waiting comes first, the longest waiting first
		"wait-old": "wait-new",
		"wait-new": "wait-old",
	} {
		if got := NextAttention(ss, shown); got == nil || got.ID != want {
			t.Errorf("shown %q: got %v, want %s", shown, got, want)
		}
	}
	if got := NextAttention([]*Session{done, seen, errS}, ""); got == nil || got.ID != "err" {
		t.Errorf("an error comes before an unseen finished turn: got %v", got)
	}
	if got := NextAttention([]*Session{done, seen}, "done"); got != nil {
		t.Errorf("only the shown one needs attention: got %s", got.ID)
	}
	if got := NextAttention([]*Session{seen, archived}, ""); got != nil {
		t.Errorf("nothing needs you: got %s", got.ID)
	}
}

func TestAttention(t *testing.T) {
	last := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	since := last.Add(-time.Hour)
	cases := []struct {
		s    Session
		want *Attention
	}{
		{Session{Status: StatusWaiting, StatusSince: since}, &Attention{1, NeedsWaiting, since}},
		{Session{Status: StatusError, StatusSince: since}, &Attention{2, NeedsError, since}},
		{Session{Status: StatusDone, Unseen: true, StatusSince: since}, &Attention{3, NeedsUnseenDone, since}},
		{Session{Status: StatusDone, Unseen: true, LastEventAt: last}, &Attention{3, NeedsUnseenDone, last}}, // from before status_since
		{Session{Status: StatusDone}, nil},
		{Session{Status: StatusBusy, Unseen: true}, nil},
		{Session{Status: StatusArchived}, nil},
	}
	for _, tc := range cases {
		got := tc.s.Attention()
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s unseen=%v: got %+v, want %+v", tc.s.Status, tc.s.Unseen, got, tc.want)
		}
	}
	// Ties go by id, and sessions that don't need you come last.
	a := &Session{ID: "a", Status: StatusWaiting, StatusSince: since}
	b := &Session{ID: "b", Status: StatusWaiting, StatusSince: since}
	idle := &Session{ID: "0", Status: StatusBusy}
	if !AttentionBefore(a, b) || AttentionBefore(b, a) || !AttentionBefore(a, idle) || AttentionBefore(idle, a) || AttentionBefore(idle, idle) {
		t.Error("AttentionBefore: want a < b < idle")
	}
}
func TestViewCollapsesGroupsByID(t *testing.T) {
	groups := []*Group{{ID: "g", Name: "G", SortKey: 1}}
	ss := []*Session{{ID: "a", GroupID: "g"}, {ID: "b"}}
	rows := Layout(ss, groups, ViewState{Mode: "groups", Collapsed: []string{"g"}})
	if !rows[0].Collapsed || rows[1].Group != nil || rows[2].Session.ID != "b" || rows[2].Number != 1 {
		t.Errorf("collapsed by the view: %+v", rows)
	}
}

// A group inside another comes after its parent's own sessions, a level in;
// a collapsed parent hides it, and counts everything under it (PAN-189).
func TestLayoutNestsGroups(t *testing.T) {
	parent := &Group{ID: "p", Name: "Backend", SortKey: 10}
	child := &Group{ID: "c", Name: "Workers", ParentID: "p", SortKey: 20}
	other := &Group{ID: "o", Name: "Docs", SortKey: 30}
	lost := &Group{ID: "l", Name: "Lost", ParentID: "gone", SortKey: 40}
	ss := []*Session{
		{ID: "a", GroupID: "p", CreatedAt: at(1)},
		{ID: "b", GroupID: "c", Status: StatusWaiting, CreatedAt: at(2)},
		{ID: "d", GroupID: "o", CreatedAt: at(3)},
	}
	gs := []*Group{child, other, parent, lost}
	rows := Layout(ss, gs, ViewState{Mode: "groups"})
	if got := render(rows); got != "[Backend] 1:a [Workers] 2:b [Docs] 3:d [Lost]" {
		t.Errorf("nested: %s", got)
	}
	if rows[0].Depth != 0 || rows[2].Depth != 1 || rows[0].Count != 2 || rows[0].Urgent != StatusWaiting {
		t.Errorf("parent and child headers: %+v %+v", rows[0], rows[2])
	}
	rows = Layout(ss, gs, ViewState{Mode: "groups", Collapsed: []string{"p"}})
	if got := render(rows); got != "[Backend +] [Docs] 1:d [Lost]" {
		t.Errorf("collapsed parent: %s", got)
	}
	if rows[0].Count != 2 || rows[0].Urgent != StatusWaiting {
		t.Errorf("a collapsed parent counts what's under it: %+v", rows[0])
	}
	// Filtering keeps a parent whose child matches.
	ss[1].Name = "flaky"
	if got := render(Layout(ss, gs, ViewState{Mode: "groups", Filter: "flaky"})); got != "[Backend] [Workers] 1:b" {
		t.Errorf("filtered: %s", got)
	}
}
