package dbtest_test

import (
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
)

func TestSnapshotIgnoresPhysicalOrder(t *testing.T) {
	pool := dbtest.NewPool(t)
	tenant, _ := dbtest.NewTenant(t, pool, "快照")
	tx := dbtest.AppTx(t, pool, tenant)
	dbtest.Exec(t, tx, "CREATE TEMP TABLE snapshot_rows (code text, amount integer) ON COMMIT DROP")
	dbtest.Exec(t, tx, "INSERT INTO snapshot_rows VALUES ('b',2),('a',1)")
	before := dbtest.SnapshotTx(t, tx, "snapshot_rows")
	dbtest.Exec(t, tx, "DELETE FROM snapshot_rows")
	dbtest.Exec(t, tx, "INSERT INTO snapshot_rows VALUES ('a',1),('b',2)")
	if dbtest.SnapshotTx(t, tx, "snapshot_rows") != before {
		t.Fatal("physical row order changed snapshot")
	}
	dbtest.Exec(t, tx, "UPDATE snapshot_rows SET amount=3 WHERE code='a'")
	if dbtest.SnapshotTx(t, tx, "snapshot_rows") == before {
		t.Fatal("snapshot missed a row change")
	}
}
