package tui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/brettmostert/agent-command/internal/core"
)

// View is the terminal UI's own view of the list. The daemon keeps no view:
// it's kept in a file of the terminal UI's own (Options.ViewFile).
type View struct {
	Mode     string `json:"mode,omitempty"` // "groups" (the default) or "list"
	Sort     string `json:"sort,omitempty"` // a key of sorts; "" is "created"
	Desc     bool   `json:"desc,omitempty"`
	Archived bool   `json:"archived,omitempty"` // show archived sessions and groups
	// Collapsed are the collapsed groups' ids, by host ("" for this
	// machine): a group's id is unique only on its own host.
	Collapsed map[string][]string `json:"collapsed,omitempty"`
}

// sorts are the sort keys, in the order s goes through them.
var sorts = []string{"created", "name", "activity", "status", "cwd"}

// LoadView reads the view in path. A missing file is the default view; one
// that can't be read is too, so a bad file never stops the terminal UI.
func LoadView(path string) View {
	var v View
	if path == "" {
		return v
	}
	b, err := os.ReadFile(path) // #nosec G304 -- the terminal UI's own file, in acmd's config folder
	if err != nil {
		return v
	}
	if json.Unmarshal(b, &v) != nil {
		return View{}
	}
	return v
}

// SaveView writes v to path, making its folder if it's missing.
func SaveView(path string, v View) error {
	if path == "" {
		return nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// layout is the view for host's sessions, with the filter typed.
func (v View) layout(host, filter string) core.ViewState {
	return core.ViewState{Mode: v.mode(), Sort: v.sort(), Desc: v.Desc, Filter: filter,
		ShowArchived: v.Archived, Collapsed: v.Collapsed[host]}
}

func (v View) mode() string {
	if v.Mode == "list" {
		return "list"
	}
	return "groups"
}

func (v View) sort() string {
	if slices.Contains(sorts, v.Sort) {
		return v.Sort
	}
	return "created"
}

// setCollapsed collapses or expands host's group id.
func (v *View) setCollapsed(host, id string, collapsed bool) {
	ids := slices.DeleteFunc(slices.Clone(v.Collapsed[host]), func(x string) bool { return x == id })
	if collapsed {
		ids = append(ids, id)
	}
	if v.Collapsed == nil {
		v.Collapsed = map[string][]string{}
	}
	if len(ids) == 0 {
		delete(v.Collapsed, host)
	} else {
		v.Collapsed[host] = ids
	}
}

// nextSort is the sort after this one.
func (v View) nextSort() string {
	i := slices.Index(sorts, v.sort())
	return sorts[(i+1)%len(sorts)]
}
