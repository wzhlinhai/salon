package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/storetime"
)

// NewPool 解析 DSN 并返回按门店时区扫描 timestamptz 的连接池。
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	return NewPoolWithConfig(ctx, cfg)
}

// NewPoolWithConfig 在已解析的连接池配置上注册门店时区并创建连接池。
func NewPoolWithConfig(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
	useStoreTimeZone(cfg)
	return pgxpool.NewWithConfig(ctx, cfg)
}

// Connect 打开一条按门店时区扫描 timestamptz 的连接。
func Connect(ctx context.Context, dsn string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	registerStoreTimeZone(conn)
	return conn, nil
}

func useStoreTimeZone(cfg *pgxpool.Config) {
	previous := cfg.AfterConnect
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if previous != nil {
			if err := previous(ctx, conn); err != nil {
				return err
			}
		}
		registerStoreTimeZone(conn)
		return nil
	}
}

func registerStoreTimeZone(conn *pgx.Conn) {
	conn.TypeMap().RegisterType(&pgtype.Type{
		Name:  "timestamptz",
		OID:   pgtype.TimestamptzOID,
		Codec: &pgtype.TimestamptzCodec{ScanLocation: storetime.Location},
	})
}
