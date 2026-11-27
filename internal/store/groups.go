package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
)

// Errors for group rules.
var (
	ErrDuplicateName = errors.New("a group with that name already exists")
	ErrEmptyName     = errors.New("group name is empty")
)

// Palette is the round-robin color order for new groups.
var Palette = []string{"blue", "magenta", "cyan", "yellow", "green", "red"}

const groupCols = `id, name, color, sort_key, collapsed, created_at, archived_at, path, parent_id`

// CreateGroup adds a group. An empty color is assigned from Palette; new
// groups sort after existing ones.
func (s *Store) CreateGroup(ctx context.Context, name, color string) (*core.Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrEmptyName
	}
	g := &core.Group{ID: NewID(), Name: name, Color: color, CreatedAt: s.now().UTC()}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var count, maxKey int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(max(sort_key), 0) FROM groups`).Scan(&count, &maxKey); err != nil {
			return err
		}
		if g.Color == "" {
			g.Color = Palette[count%len(Palette)]
		}
		g.SortKey = maxKey + 10
		_, err := tx.ExecContext(ctx, `INSERT INTO groups (`+groupCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
			g.ID, g.Name, g.Color, g.SortKey, 0, ms(g.CreatedAt), 0, "", "")
		return mapUnique(err)
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

// Group returns a group by id.
func (s *Store) Group(ctx context.Context, id string) (*core.Group, error) {
	return scanGroup(s.q.QueryRowContext(ctx, `SELECT `+groupCols+` FROM groups WHERE id = ?`, id))
}

// GroupByName finds a non-archived group by name, case-insensitively.
func (s *Store) GroupByName(ctx context.Context, name string) (*core.Group, error) {
	return scanGroup(s.q.QueryRowContext(ctx, `SELECT `+groupCols+` FROM groups
		WHERE lower(name) = lower(?) AND archived_at = 0`, strings.TrimSpace(name)))
}

// Groups lists groups in display order (sort_key).
func (s *Store) Groups(ctx context.Context, includeArchived bool) ([]*core.Group, error) {
	q := `SELECT ` + groupCols + ` FROM groups`
	if !includeArchived {
		q += ` WHERE archived_at = 0`
	}
	rows, err := s.q.QueryContext(ctx, q+` ORDER BY sort_key, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RenameGroup renames a group.
func (s *Store) RenameGroup(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrEmptyName
	}
	return s.updateGroup(ctx, id, `UPDATE groups SET name = ? WHERE id = ?`, name, id)
}

// SetGroupColor changes a group's color.
func (s *Store) SetGroupColor(ctx context.Context, id, color string) error {
	return s.exec1(ctx, `UPDATE groups SET color = ? WHERE id = ?`, color, id)
}

// SetGroupPath makes a group a folder with path, or ("") a group again.
// Making sure the path exists is the caller's job: it's on the group's host.
func (s *Store) SetGroupPath(ctx context.Context, id, path string) error {
	return s.updateGroup(ctx, id, `UPDATE groups SET path = ? WHERE id = ?`, path, id)
}

// SetGroupParent puts a group inside parent ("" is the top), at sortKey
// among its siblings. The rules (two levels, no cycles) are the caller's.
func (s *Store) SetGroupParent(ctx context.Context, id, parent string, sortKey int) error {
	return s.updateGroup(ctx, id, `UPDATE groups SET parent_id = ?, sort_key = ? WHERE id = ?`, parent, sortKey, id)
}

// SetGroupCollapsed records a group's collapsed state.
func (s *Store) SetGroupCollapsed(ctx context.Context, id string, collapsed bool) error {
	return s.exec1(ctx, `UPDATE groups SET collapsed = ? WHERE id = ?`, b2i(collapsed), id)
}

// SetGroupSortKey moves a group in the display order.
func (s *Store) SetGroupSortKey(ctx context.Context, id string, key int) error {
	return s.updateGroup(ctx, id, `UPDATE groups SET sort_key = ? WHERE id = ?`, key, id)
}

// DeleteGroup removes a group. Its sessions are left in no group: archiving
// them first is the caller's job (it involves stopping them).
func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := groupExists(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET group_id = NULL WHERE group_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
		return err
	})
}

// SetGroupArchived archives or unarchives a group. Archiving the group's
// sessions is the caller's job (it involves stopping them).
func (s *Store) SetGroupArchived(ctx context.Context, id string, archived bool) error {
	at := int64(0)
	if archived {
		at = ms(s.now())
	}
	return s.updateGroup(ctx, id, `UPDATE groups SET archived_at = ? WHERE id = ?`, at, id)
}

func (s *Store) updateGroup(ctx context.Context, id, q string, args ...any) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := groupExists(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, q, args...)
		return mapUnique(err)
	})
}

func (s *Store) exec1(ctx context.Context, q string, args ...any) error {
	res, err := s.q.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func groupExists(ctx context.Context, tx *sql.Tx, id string) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM groups WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func liveGroupExists(ctx context.Context, tx *sql.Tx, id string) error {
	var archived int64
	err := tx.QueryRowContext(ctx, `SELECT archived_at FROM groups WHERE id = ?`, id).Scan(&archived)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("group %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if archived != 0 {
		return fmt.Errorf("group %s is archived", id)
	}
	return nil
}

func mapUnique(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrDuplicateName
	}
	return err
}

func scanGroup(row scanner) (*core.Group, error) {
	var (
		g                   core.Group
		collapsed           int
		created, archivedAt int64
	)
	err := row.Scan(&g.ID, &g.Name, &g.Color, &g.SortKey, &collapsed, &created, &archivedAt, &g.Path, &g.ParentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.Collapsed = collapsed != 0
	g.CreatedAt, g.ArchivedAt = fromMS(created), fromMS(archivedAt)
	return &g, nil
}
