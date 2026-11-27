package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Row is one line of the session list: a group header or
// a session. Number is the 1-based shortcut number of visible session rows
// (0 for headers and sessions in collapsed groups).
type Row struct {
	// Group is the header's group, and a session row's group. It's nil for
	// the "No group" header and for sessions in no group.
	Group   *Group
	Session *Session // nil for header rows
	Number  int

	// Header rows only.
	Depth     int    // 0 for a top-level group, 1 for one inside another
	Count     int    // sessions in the group and the groups inside it (after filtering)
	Urgent    Status // most urgent status in the group, for collapsed headers
	Collapsed bool
}

// IsHeader reports whether this is a header row: a group's, or "No group".
func (r Row) IsHeader() bool { return r.Session == nil }

// NoGroupTitle is what the header over sessions in no group says. It isn't a group.
const NoGroupTitle = "No group"

// Title is a header's text: its group's name, or NoGroupTitle.
func (r Row) Title() string {
	if r.Group == nil {
		return NoGroupTitle
	}
	return r.Group.Name
}

// Layout orders and numbers sessions for display according to a client's
// view: list or group view, sort key and direction, filter and collapse.
// Numbers are the client's own shortcuts: commands take ids.
func Layout(sessions []*Session, groups []*Group, vs ViewState) []Row {
	byID := map[string]*Group{}
	for _, g := range groups {
		byID[g.ID] = g
	}
	// A session's group, while it shows; nil is no group.
	groupOf := func(s *Session) *Group {
		if g, ok := byID[s.GroupID]; ok && (g.ArchivedAt.IsZero() || vs.ShowArchived) {
			return g
		}
		return nil
	}

	f := parseFilter(vs.Filter)
	var visible []*Session
	for _, s := range sessions {
		if s.Status == StatusArchived && !vs.ShowArchived {
			continue
		}
		if f.match(s, groupOf(s)) {
			visible = append(visible, s)
		}
	}
	sortSessions(visible, vs.Sort, vs.Desc)

	liveGroups := 0
	for _, g := range groups {
		if g.ArchivedAt.IsZero() {
			liveGroups++
		}
	}
	// List view, or group view with no groups: no headers.
	if vs.Mode == "list" || liveGroups == 0 {
		rows := make([]Row, len(visible))
		for i, s := range visible {
			rows[i] = Row{Group: groupOf(s), Session: s, Number: i + 1}
		}
		return rows
	}

	members := map[string][]*Session{}
	var ungrouped []*Session
	for _, s := range visible {
		if g := groupOf(s); g != nil {
			members[g.ID] = append(members[g.ID], s)
		} else {
			ungrouped = append(ungrouped, s)
		}
	}

	// Groups inside others come after their parent's own sessions. One
	// whose parent isn't shown is at the top.
	ordered := orderGroups(groups, vs.ShowArchived)
	shown := map[string]bool{}
	for _, g := range ordered {
		shown[g.ID] = true
	}
	children := map[string][]*Group{}
	var top []*Group
	for _, g := range ordered {
		if g.ParentID != "" && shown[g.ParentID] {
			children[g.ParentID] = append(children[g.ParentID], g)
		} else {
			top = append(top, g)
		}
	}
	var rows []Row
	n := 0
	var add func(g *Group, depth int)
	add = func(g *Group, depth int) {
		all := append([]*Session{}, members[g.ID]...)
		for _, c := range children[g.ID] {
			all = append(all, members[c.ID]...)
		}
		if f.active() && len(all) == 0 {
			return // filtering hides groups without matches
		}
		collapsed := vs.IsCollapsed(g.ID) && !f.active() // matches expand while filtering
		rows = append(rows, Row{Group: g, Depth: depth, Count: len(all), Urgent: mostUrgent(all), Collapsed: collapsed})
		if collapsed {
			return // a collapsed parent hides its children too
		}
		for _, s := range members[g.ID] {
			n++
			rows = append(rows, Row{Group: g, Session: s, Number: n})
		}
		for _, c := range children[g.ID] {
			add(c, depth+1)
		}
	}
	for _, g := range top {
		add(g, 0)
	}
	// Sessions in no group come last, under a header that isn't a group: it
	// never collapses.
	if len(ungrouped) > 0 {
		rows = append(rows, Row{Count: len(ungrouped), Urgent: mostUrgent(ungrouped)})
		for _, s := range ungrouped {
			n++
			rows = append(rows, Row{Session: s, Number: n})
		}
	}
	return rows
}

func orderGroups(groups []*Group, showArchived bool) []*Group {
	var out []*Group
	for _, g := range groups {
		if g.ArchivedAt.IsZero() || showArchived {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.SortKey != b.SortKey {
			return a.SortKey < b.SortKey
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return out
}

// statusRank orders statuses by urgency.
func statusRank(s *Session) int {
	switch s.Status {
	case StatusWaiting:
		return 0
	case StatusError:
		return 1
	case StatusBusy:
		return 2
	case StatusDone:
		if s.Unseen {
			return 3
		}
		return 4
	case StatusStarting, StatusUnknown:
		return 5
	case StatusPaused, StatusFinished:
		return 6
	default:
		return 7
	}
}

func mostUrgent(ss []*Session) Status {
	best, rank := Status(""), 99
	for _, s := range ss {
		if r := statusRank(s); r < rank {
			best, rank = s.Status, r
		}
	}
	return best
}

func sortSessions(ss []*Session, key string, desc bool) {
	less := func(a, b *Session) int {
		switch key {
		case "name":
			return naturalCompare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "activity":
			return a.LastEventAt.Compare(b.LastEventAt)
		case "status":
			return statusRank(a) - statusRank(b)
		case "cwd":
			if c := strings.Compare(strings.ToLower(filepath.Base(a.Cwd)), strings.ToLower(filepath.Base(b.Cwd))); c != 0 {
				return c
			}
			return naturalCompare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		default: // created
			return 0
		}
	}
	sort.SliceStable(ss, func(i, j int) bool {
		a, b := ss[i], ss[j]
		c := less(a, b)
		if c == 0 {
			// Stable tie-break: creation order, then id.
			if c = a.CreatedAt.Compare(b.CreatedAt); c == 0 {
				c = strings.Compare(a.ID, b.ID)
			}
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
}

// naturalCompare compares strings treating digit runs as numbers, so
// "proj-9" < "proj-10". Digit runs are compared by length (after leading
// zeros) and then lexically, so arbitrarily long numbers can't overflow.
func naturalCompare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if isDigit(ra[i]) && isDigit(rb[j]) {
			si, sj := i, j
			for i < len(ra) && isDigit(ra[i]) {
				i++
			}
			for j < len(rb) && isDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if c := cmpInt(len(na), len(nb)); c != 0 {
				return c
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			continue
		}
		if ra[i] != rb[j] {
			return cmpInt(int(ra[i]), int(rb[j]))
		}
		i++
		j++
	}
	return cmpInt(len(ra)-i, len(rb)-j)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// filter is a parsed filter string: plain terms match name, description,
// cwd and group name; g:<text> terms match the group name. All terms must
// match.
type filter struct{ text, group []string }

func parseFilter(s string) filter {
	var f filter
	for _, t := range strings.Fields(strings.ToLower(s)) {
		if g, ok := strings.CutPrefix(t, "g:"); ok {
			if g != "" {
				f.group = append(f.group, g)
			}
			continue
		}
		f.text = append(f.text, t)
	}
	return f
}

func (f filter) active() bool { return len(f.text)+len(f.group) > 0 }

func (f filter) match(s *Session, g *Group) bool {
	name := NoGroupTitle // g:no matches sessions in no group
	if g != nil {
		name = g.Name
	}
	gname := strings.ToLower(name)
	for _, t := range f.group {
		if !strings.Contains(gname, t) {
			return false
		}
	}
	hay := strings.ToLower(strings.Join([]string{s.Name, s.Description, s.Cwd, name}, "\x00"))
	for _, t := range f.text {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// Selector errors.
var ErrNoMatch = errors.New("no session matches")

// Resolve finds a session by its acmd id or its Claude session id. Nothing
// else picks a session: a name or a number can mean another one later.
func Resolve(all []*Session, id string) (*Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrNoMatch
	}
	for _, s := range all {
		if s.ID == id || s.ClaudeSessionID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("%w %q: give its id (acmd find <name> finds it)", ErrNoMatch, id)
}

// Find is the sessions whose name has text in it, ignoring case.
func Find(all []*Session, text string) []*Session {
	text = strings.ToLower(strings.TrimSpace(text))
	var out []*Session
	for _, s := range all {
		if text != "" && strings.Contains(strings.ToLower(s.Name), text) {
			out = append(out, s)
		}
	}
	return out
}

// NextAttention picks the session that most needs you, other than shown:
// the lowest attention rank, then the one waiting longest (AttentionBefore).
// Archived sessions are left out.
func NextAttention(sessions []*Session, shown string) *Session {
	var best *Session
	for _, s := range sessions {
		if s.ID == shown || s.Status == StatusArchived || s.Attention() == nil {
			continue
		}
		if best == nil || AttentionBefore(s, best) {
			best = s
		}
	}
	return best
}
