package dataio_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"

	"github.com/wzhlinhai/salon/backend/internal/dataio"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/optest"
)

func TestExportUsesSnapshot(t *testing.T) {
	f := open(t)
	ctx := context.Background()
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Exec(t, tx, `LOCK TABLE cards IN ACCESS EXCLUSIVE MODE`)
	customerID, cardID := uuid.New(), uuid.New()
	dbtest.Exec(t, tx, `INSERT INTO customers(id,tenant_id,store_id,name,created_by) VALUES($1,$2,$3,'快照并发顾客',$4)`, customerID, f.Res.TenantID, f.Res.StoreID, f.Res.OwnerID)
	dbtest.Exec(t, tx, `INSERT INTO cards(id,tenant_id,store_id,customer_id,card_type_id,opened_at,created_by) VALUES($1,$2,$3,$4,$5,clock_timestamp(),$6)`, cardID, f.Res.TenantID, f.Res.StoreID, customerID, uuid.MustParse(f.storedType), f.Res.OwnerID)
	dbtest.Exec(t, tx, `INSERT INTO card_ledger(tenant_id,store_id,card_id,kind,principal_before,principal_delta,principal_after,bonus_before,bonus_delta,bonus_after,business_date,actor_id) VALUES($1,$2,$3,'open',0,1,1,0,0,0,current_date,$4)`, f.Res.TenantID, f.Res.StoreID, cardID, f.Res.OwnerID)

	done := make(chan optest.Result, 1)
	var out bytes.Buffer
	go func() {
		err := dataio.Export(ctx, &out, f.Pool, f.MustCaller(f.Owner), time.Now())
		status := http.StatusOK
		if err != nil {
			status = http.StatusInternalServerError
		}
		done <- optest.Result{Status: status, Err: err}
	}()
	optest.AssertWaiting(t, f.Pool, done, 1)
	dbtest.Commit(t, tx)
	optest.WaitResult(t, done, http.StatusOK)

	file, err := excelize.OpenReader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	rows, err := file.GetRows("储值卡")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("stored rows = %d, want header only (concurrent card leaked into one sheet)", len(rows)-1)
	}
	customers, err := file.GetRows("顾客")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range customers[1:] {
		if row[0] == customerID.String() {
			t.Fatal("concurrent customer leaked into the customer sheet")
		}
	}
}
