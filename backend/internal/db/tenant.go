package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

// ErrNoTenant 表示调用方没有提供租户。
var ErrNoTenant = errors.New("tenant id is required")

// InTenant 在一个事务中以 salon_app 角色、指定租户的上下文执行 fn。
// 事务内的所有查询都受行级安全约束，只能读写该租户的数据。
// fn 返回错误时事务回滚，否则提交。
func InTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, fn func(q *dbgen.Queries) error) error {
	return inTenant(ctx, pool, tenantID, pgx.TxOptions{}, fn)
}

// InTenantReadOnly 在可重复读的只读事务中执行 fn；任何写入都会被 PostgreSQL 拒绝。
func InTenantReadOnly(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, fn func(q *dbgen.Queries) error) error {
	return inTenant(ctx, pool, tenantID, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

// InTenantSnapshot 在可重复读事务中读取同一租户的一致快照。
func InTenantSnapshot(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, fn func(q *dbgen.Queries) error) error {
	return inTenant(ctx, pool, tenantID, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, fn)
}

func inTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, options pgx.TxOptions, fn func(q *dbgen.Queries) error) error {
	if tenantID == uuid.Nil {
		return ErrNoTenant
	}
	return pgx.BeginTxFunc(ctx, pool, options, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE salon_app"); err != nil {
			return fmt.Errorf("set role: %w", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
			return fmt.Errorf("set tenant: %w", err)
		}
		return fn(dbgen.New(tx))
	})
}
