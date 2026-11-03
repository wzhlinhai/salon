package commission

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
)

func TestSelectCommissionVersionSaveSequence(t *testing.T) {
	saved := date("2026-10-01").Time
	older := version(1, "2026-10-05", saved, CommissionScheme{})
	older.ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	newer := version(2, "2026-10-05", saved.Add(-time.Hour), CommissionScheme{})
	newer.ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	for _, rows := range [][]dbgen.CommissionSchemeVersion{{older, newer}, {newer, older}} {
		got, err := selectVersion(rows, date("2026-10-05").Time, nil)
		if err != nil || got.Version != 2 || got.ID != newer.ID {
			t.Fatal(got, err)
		}
	}
}
