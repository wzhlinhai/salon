package commission

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/inforce"
)

// selectVersion 选出营业日 day 执行的方案版本；cutoff 不为空时只考虑这一时刻及以前保存的版本。
func selectVersion(rows []dbgen.CommissionSchemeVersion, day time.Time, cutoff *time.Time) (dbgen.CommissionSchemeVersion, error) {
	changes := make([]inforce.Change[dbgen.CommissionSchemeVersion], 0, len(rows))
	for _, v := range rows {
		if cutoff != nil && v.SavedAt.Time.After(*cutoff) {
			continue
		}
		changes = append(changes, inforce.Change[dbgen.CommissionSchemeVersion]{From: v.EffectiveFrom.Time, Seq: int64(v.Version), SavedAt: v.SavedAt.Time, ID: v.ID, Value: v})
	}
	c, ok := inforce.New(changes).At(day)
	if !ok {
		return dbgen.CommissionSchemeVersion{}, pgx.ErrNoRows
	}
	return c.Value, nil
}
