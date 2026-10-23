package settings

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// Snapshot 是已保存的规则快照，包含下单所需的 ID、营业日和同一份规则。
type Snapshot struct {
	ID           uuid.UUID
	BusinessDate time.Time
	Rules        Rules
}

// TakeSnapshot 保存时刻 at 所在营业日的全部设置，并返回 ID、营业日和规则。
// 单据保存 ID；之后用 LoadSnapshot 读回，使历史单据不随设置变化。
func TakeSnapshot(ctx context.Context, q *dbgen.Queries, tenantID, storeID uuid.UUID, at time.Time) (Snapshot, error) {
	day, err := BusinessDate(ctx, q, storeID, at)
	if err != nil {
		return Snapshot{}, err
	}
	rules, err := Effective(ctx, q, storeID, day)
	if err != nil {
		return Snapshot{}, err
	}
	b, err := json.Marshal(rules.values)
	if err != nil {
		return Snapshot{}, err
	}
	params := dbgen.SaveSettingsSnapshotParams{TenantID: tenantID, StoreID: storeID, Rules: b}
	id, err := q.SaveSettingsSnapshot(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		// 另一个事务同时保存了同样的快照，它提交后这条语句才能看到。
		// 依赖 db.InTenant 的 READ COMMITTED：每条语句取新的快照。
		id, err = q.SaveSettingsSnapshot(ctx, params)
	}
	return Snapshot{ID: id, BusinessDate: day, Rules: rules}, err
}

// LoadSnapshot 读回门店 storeID 的快照中的设置。快照之后才加入目录的设置项按系统默认做法。
func LoadSnapshot(ctx context.Context, q *dbgen.Queries, storeID, id uuid.UUID) (Rules, error) {
	row, err := q.GetSettingsSnapshot(ctx, dbgen.GetSettingsSnapshotParams{ID: id, StoreID: storeID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Rules{}, ops.ErrNotFound
	}
	if err != nil {
		return Rules{}, err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(row.Rules, &values); err != nil {
		return Rules{}, err
	}
	return rulesFrom(values), nil
}
