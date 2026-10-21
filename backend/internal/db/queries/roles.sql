-- name: CreateRole :one
INSERT INTO roles (tenant_id, store_id, name, is_owner) VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRole :one
SELECT * FROM roles WHERE id = $1;

-- name: LockRole :one
SELECT id FROM roles WHERE id = $1 AND store_id = $2 FOR UPDATE;

-- name: ListRoles :many
SELECT * FROM roles WHERE store_id = $1 ORDER BY is_owner DESC, created_at, id;

-- name: RenameRole :execrows
UPDATE roles SET name = $2 WHERE id = $1 AND NOT is_owner;

-- name: DeleteRole :execrows
DELETE FROM roles WHERE id = $1 AND NOT is_owner;

-- name: LockOwnerRole :one
SELECT id FROM roles WHERE store_id = $1 AND is_owner FOR NO KEY UPDATE;

-- name: ListRolePermissions :many
SELECT * FROM role_permissions WHERE role_id = ANY(@role_ids::uuid[]) ORDER BY role_id, permission;

-- DeleteRolePermission 只删除角色被收回的一项权限。
-- name: DeleteRolePermission :exec
DELETE FROM role_permissions WHERE role_id = $1 AND permission = $2;

-- UpdateRolePermission 只修改角色已有权限的数据范围和时间窗口。
-- name: UpdateRolePermission :exec
UPDATE role_permissions SET scope = $3, window_days = $4 WHERE role_id = $1 AND permission = $2;

-- name: AddRolePermission :exec
INSERT INTO role_permissions (tenant_id, store_id, role_id, permission, scope, window_days)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListStaffRoles :many
SELECT r.* FROM roles r JOIN staff_roles sr ON sr.role_id = r.id
WHERE sr.staff_id = $1 ORDER BY r.is_owner DESC, r.created_at, r.id;

-- name: ListAllStaffRoles :many
SELECT sr.staff_id, sr.role_id FROM staff_roles sr WHERE sr.store_id = $1;

-- name: AddStaffRole :exec
INSERT INTO staff_roles (tenant_id, store_id, staff_id, role_id) VALUES ($1, $2, $3, $4);

-- DeleteStaffRole 只删除员工被收回的一个角色。
-- name: DeleteStaffRole :exec
DELETE FROM staff_roles WHERE staff_id = $1 AND role_id = $2;

-- name: DeleteStaffRoles :exec
DELETE FROM staff_roles WHERE staff_id = $1;

-- name: CountActiveOwners :one
SELECT count(*) FROM staff s
JOIN staff_roles sr ON sr.staff_id = s.id
JOIN roles r ON r.id = sr.role_id
WHERE r.store_id = $1 AND r.is_owner AND s.active;

-- name: ListRoleHolders :many
SELECT s.id, s.name FROM staff s JOIN staff_roles sr ON sr.staff_id = s.id
WHERE sr.store_id = $1 AND sr.role_id = $2
ORDER BY s.id;
