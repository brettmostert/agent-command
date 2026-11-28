package ipc

import (
	"slices"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
)

// Where a session is, for SessionFilter.On: this machine, or any known
// host. Any other value is a host's name.
const (
	OnLocal  = "local"
	OnRemote = "remote"
)

// SessionListRequest is session.list's filter, and the page it asks for.
type SessionListRequest struct {
	Filter SessionFilter `json:"filter"`
	Page
}

// SessionFilter narrows a list of sessions. Every key that is set must
// match, and a key with several values matches any of them. The empty filter
// is every session that isn't archived, here and on every known host.
type SessionFilter struct {
	// On is where: OnLocal, OnRemote, or a host's name. None is everywhere.
	On []string `json:"on,omitempty"`
	// Resumable lists what can be resumed: the stopped sessions, and the
	// agents' own sessions acmd doesn't have (one run in a plain terminal,
	// or purged). Those have no acmd id: resuming one makes one.
	Resumable bool `json:"resumable,omitempty"`
	// Harness is harness names, as each session's own machine has them,
	// ignoring case ("Work", "Terminal").
	Harness []string `json:"harness,omitempty"`
	// Group is group ids.
	Group []string `json:"group,omitempty"`
	// Status is statuses ("busy", "waiting").
	Status []string `json:"status,omitempty"`
	// Archived includes archived sessions.
	Archived bool `json:"archived,omitempty"`
}

// Here says whether the filter takes in this machine's sessions.
func (f SessionFilter) Here() bool { return len(f.On) == 0 || slices.Contains(f.On, OnLocal) }

// OnHost says whether the filter takes in a known host's sessions.
func (f SessionFilter) OnHost(name string) bool {
	return len(f.On) == 0 || slices.Contains(f.On, OnRemote) || slices.Contains(f.On, name)
}

// Match says whether s passes the filter, but for Resumable, which only
// the session's own daemon can tell. Its machine is s.Host ("" for this one).
func (f SessionFilter) Match(s *core.Session) bool {
	if s.Host == "" && !f.Here() || s.Host != "" && !f.OnHost(s.Host) {
		return false
	}
	if s.Status == core.StatusArchived && !f.Archived {
		return false
	}
	if len(f.Harness) > 0 && !slices.ContainsFunc(f.Harness, func(h string) bool {
		return strings.EqualFold(strings.TrimSpace(h), harnessName(s))
	}) {
		return false
	}
	if len(f.Group) > 0 && !slices.Contains(f.Group, s.GroupID) {
		return false
	}
	return len(f.Status) == 0 || slices.Contains(f.Status, string(s.Status))
}

func harnessName(s *core.Session) string {
	if s.HarnessName != "" {
		return s.HarnessName
	}
	return core.KindTitle(s.HarnessKind)
}
