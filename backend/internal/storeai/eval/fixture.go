package storeaieval

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai/eval"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy"
	"github.com/wzhlinhai/salon/backend/internal/groupbuy/fakeprovider"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

// caseTB 让每条用例单独收尾，避免评测时堆积数据库资源。
type caseTB struct {
	testing.TB
	cleanups []func()
}

func (t *caseTB) Cleanup(f func()) { t.cleanups = append(t.cleanups, f) }

func (t *caseTB) runCleanups() {
	for i := len(t.cleanups) - 1; i >= 0; i-- {
		t.cleanups[i]()
	}
	t.cleanups = nil
}

func seedClassifier(t testing.TB) *eval.Fixture {
	t.Helper()
	return eval.Seed(t)
}

func seedAgent(t testing.TB) *eval.Fixture {
	t.Helper()
	f := eval.Seed(t)
	shop := f.Shop
	f.Refs["staff:王老板"] = shop.Res.OwnerID.String()
	shop.Groupbuy.Register("douyin", fakeprovider.New("douyin", f.Now))

	for i, key := range []string{"eval-douyin-1", "eval-douyin-2"} {
		shop.Call(t, shop.Owner, "redeem_coupon", groupbuy.CouponRequest{
			Platform: "douyin", CouponCode: key, Key: key,
		}, 200)
		redemption := shop.Call(t, shop.Owner, "get_redemption", map[string]any{"key": key}, 200)
		f.Refs[fmt.Sprintf("redemption:抖音%d", i+1)] = redemption.Body["id"].(string)
	}

	shop.Call(t, shop.Owner, "link_coupon_order", map[string]any{
		"redemption_id": f.Refs["redemption:美团1"],
		"deal_id":       f.Refs["deal:美团剪发券"],
		"order_id":      f.Refs["order:王姐挂单"],
	}, 200)

	staffRef := func(name string) uuid.UUID { return uuid.MustParse(f.Refs["staff:"+name]) }
	douyin2 := uuid.MustParse(f.Refs["redemption:抖音2"])
	employeeRequest, err := createRequest(f, staffRef("阿峰"), douyin2, "inform")
	if err != nil {
		t.Fatal(err)
	}
	handoverRequest, err := createRequest(f, staffRef("小李"), douyin2, "inform")
	if err != nil {
		t.Fatal(err)
	}
	f.Refs["request:员工待办"] = employeeRequest.String()
	f.Refs["request:交办"] = handoverRequest.String()
	return f
}

func createRequest(f *eval.Fixture, staffID, subjectID uuid.UUID, kind string) (uuid.UUID, error) {
	var request dbgen.AiRequest
	err := db.InTenant(context.Background(), f.Shop.Pool, f.Shop.Res.TenantID, func(q *dbgen.Queries) error {
		var err error
		request, err = storeai.CreateRequest(context.Background(), q, f.Now, storeai.CreateRequestInput{
			StoreID: f.Shop.Res.StoreID, ToStaffID: staffID, Kind: kind,
			Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subjectID},
		})
		return err
	})
	return request.ID, err
}

func resolveValue(value any, f *eval.Fixture) (any, error) {
	switch v := value.(type) {
	case json.RawMessage:
		var parsed any
		if err := json.Unmarshal(v, &parsed); err != nil {
			return nil, err
		}
		return resolveValue(parsed, f)
	case string:
		return f.ResolveString(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			resolved, err := resolveValue(item, f)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			resolved, err := resolveValue(item, f)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	default:
		return value, nil
	}
}

func insertClassifierReminders(t testing.TB, f *eval.Fixture, recipient, subject uuid.UUID, reminders []storeai.ClassifierCaseReminder) {
	t.Helper()
	if len(reminders) == 0 {
		return
	}
	tx := dbtest.AppTx(t, f.Shop.Pool, f.Shop.Res.TenantID)
	for _, reminder := range reminders {
		var sentAt, withdrawnAt *time.Time
		switch reminder.Status {
		case "sent":
			sentAt = &reminder.At
		case "withdrawn":
			withdrawnAt = &reminder.At
		}
		if _, err := tx.Exec(context.Background(), `
			INSERT INTO ai_outbox (tenant_id,store_id,recipient_id,tier,subject_kind,subject_id,body,status,queued_at,sent_at,withdrawn_at)
			VALUES ($1,$2,$3,$4,'coupon_redemption',$5,$6,$7,$8,$9,$10)`,
			f.Shop.Res.TenantID, f.Shop.Res.StoreID, recipient, reminder.Tier, subject, reminder.Body,
			reminder.Status, reminder.At, sentAt, withdrawnAt); err != nil {
			dbtest.Rollback(t, tx)
			t.Fatal(err)
		}
	}
	dbtest.Commit(t, tx)
}
