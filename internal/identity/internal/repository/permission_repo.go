package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"lt-api.aleksrdvn.com/internal/identity/internal/model"
)

// PermissionRepository resolves a role's permission codes.
type PermissionRepository struct {
	pool *pgxpool.Pool
}

func NewPermissionRepository(pool *pgxpool.Pool) *PermissionRepository {
	return &PermissionRepository{pool: pool}
}

func (r *PermissionRepository) GetAllForRole(ctx context.Context, roleID int) (model.Permissions, error) {
	query := `
		SELECT permissions.code
		FROM permissions
		INNER JOIN roles_permissions ON roles_permissions.permission_id = permissions.id
		INNER JOIN roles ON roles_permissions.role_id = roles.id
		WHERE roles.id = $1
	`
	rows, err := r.pool.Query(ctx, query, roleID)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var permissions model.Permissions

	for rows.Next() {
		var permission string

		err := rows.Scan(&permission)
		if err != nil {
			return nil, err
		}

		permissions = append(permissions, permission)
	}

	return permissions, rows.Err()
}
