package identity

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Permissions []string

func (p Permissions) Include(code string) bool {
	return slices.Contains(p, code)
}

type PermissionStore struct {
	pool *pgxpool.Pool
}

func (s PermissionStore) GetAllForRole(ctx context.Context, roleID int) (Permissions, error) {
	query := `
		SELECT permissions.code
		FROM permissions
		INNER JOIN roles_permissions ON roles_permissions.permission_id = permissions.id
		INNER JOIN roles ON roles_permissions.role_id = roles.id
		WHERE roles.id = $1
	`
	rows, err := s.pool.Query(ctx, query, roleID)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var permissions Permissions

	for rows.Next() {
		var permission string

		err := rows.Scan(&permission)
		if err != nil {
			return nil, err
		}

		permissions = append(permissions, permission)
	}

	return permissions, nil
}
