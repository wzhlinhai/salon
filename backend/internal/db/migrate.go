// Package db 负责数据库连接、迁移和租户上下文。
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// newProvider 返回迁移 provider 和关闭它所用连接的函数。关闭失败不影响迁移结果，因此忽略。
func newProvider(pool *pgxpool.Pool) (*goose.Provider, func(), error) {
	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, nil, err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir,
		goose.WithDisableGlobalRegistry(true))
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("create migration provider: %w", err)
	}
	return p, func() { _ = sqlDB.Close() }, nil
}

// MigrateUp 执行全部未执行的迁移。
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) error {
	p, closeDB, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer closeDB()
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateDown 回滚最近一次迁移。
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error {
	p, closeDB, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer closeDB()
	if _, err := p.Down(ctx); err != nil {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// MigrateReset 回滚全部迁移。
func MigrateReset(ctx context.Context, pool *pgxpool.Pool) error {
	p, closeDB, err := newProvider(pool)
	if err != nil {
		return err
	}
	defer closeDB()
	if _, err := p.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("migrate reset: %w", err)
	}
	return nil
}

// MigrationVersion 返回当前已执行到的迁移版本。
func MigrationVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	p, closeDB, err := newProvider(pool)
	if err != nil {
		return 0, err
	}
	defer closeDB()
	return p.GetDBVersion(ctx)
}
