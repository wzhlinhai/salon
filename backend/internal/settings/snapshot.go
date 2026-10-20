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

// TakeSnapshot 保存时刻 at 所在营业日执行的全部设置（含系统默认做法），返回快照 ID。
// 单据在下单时调用它并保存 ID，之后用 LoadSnapshot 读回，历史单据因此不随设置变化。
func TakeSnapshot(ctx context.Context, q *dbgen.Queries, tenantID, storeID uuid.UUID, at time.Time) (uuid.UUID, error) {
	day, err := BusinessDate(ctx, q, storeID, at)
	if err != nil {
		return uuid.Nil, err
	}
	rules, err := Effective(ctx, q, storeID, day)
	if err != nil {
		return uuid.Nil, err
	}
	b, err := json.Marshal(rules.values)
	if err != nil {
		return uuid.Nil, err
	}
	params := dbgen.SaveSettingsSnapshotParams{TenantID: tenantID, StoreID: storeID, Rules: b}
	id, err := q.SaveSettingsSnapshot(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		// 另一个事务同时保存了同样的快照，它提交后这条语句才能看到。
		id, err = q.SaveSettingsSnapshot(ctx, params)
	}
	return id, err
}

// LoadSnapshot 读回快照中的设置。快照之后才加入目录的设置项按系统默认做法。
func LoadSnapshot(ctx context.Context, q *dbgen.Queries, id uuid.UUID) (Rules, error) {
	row, err := q.GetSettingsSnapshot(ctx, id)
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
