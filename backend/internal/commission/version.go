package commission

import (
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

func selectVersion(rows []dbgen.CommissionSchemeVersion, day time.Time, cutoff *time.Time) (dbgen.CommissionSchemeVersion, error) {
	var best dbgen.CommissionSchemeVersion
	for _, v := range rows {
		if v.EffectiveFrom.Time.After(day) {
			continue
		}
		if cutoff != nil && v.SavedAt.Time.After(*cutoff) {
			continue
		}
		if best.Version == 0 || v.EffectiveFrom.Time.After(best.EffectiveFrom.Time) || (v.EffectiveFrom.Time.Equal(best.EffectiveFrom.Time) && v.Version > best.Version) {
			best = v
		}
	}
	if best.Version == 0 {
		return best, pgx.ErrNoRows
	}
	return best, nil
}
