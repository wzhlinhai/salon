package dataio_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

func TestUndoImportRejectedAfterSeal(t *testing.T) {
	f := open(t)
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, settings.Location)
	f.SetNow(day)
	batch, _ := f.previewDefault()
	f.commit(batch)
	f.SetNow(day.AddDate(0, 0, 1))
	f.Call(t, f.Owner, "close_day", map[string]any{"date": "2026-10-01", "cash_counted": "0", "unionpay_actual": "0", "wechat_actual": "0", "alipay_actual": "0"}, http.StatusOK)
	f.Call(t, f.Owner, "seal_day", map[string]any{"date": "2026-10-01"}, http.StatusOK)
	before := dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "customers", "cards", "card_ledger", "card_count_ledger", "card_points_ledger", "import_batch_records", "import_batches")
	r := f.HTTPWithToken(t, f.Owner, "undo_import", map[string]any{"batch_id": batch})
	if r.Status != http.StatusConflict || r.Detail() != "该营业日已封账（2026-10-01）" {
		t.Fatal(r)
	}
	if dbtest.Snapshot(t, f.Pool, f.Res.TenantID, "customers", "cards", "card_ledger", "card_count_ledger", "card_points_ledger", "import_batch_records", "import_batches") != before {
		t.Fatal("rejected undo changed data")
	}
	f.Call(t, f.Owner, "unseal_day", map[string]any{"reason": "测试撤销"}, http.StatusOK)
	f.call("undo_import", map[string]any{"batch_id": batch})
}
