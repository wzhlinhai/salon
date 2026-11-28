package storeai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type alwaysBlockClassifier struct{}

func (alwaysBlockClassifier) Chat(context.Context, []ai.Message, []ops.Tool) (ai.Message, error) {
	return ai.Message{Role: ai.RoleAssistant, Content: `{"decision":"block","reason":"测试拦下"}`}, nil
}

// TestEveryClassifierToolIsClassified 遍历 classifierTools；新增工具只改名单却没在
// handler 里调用 classifyTool 时，这里的合法调用会真的写业务数据，测试随之失败。
func TestEveryClassifierToolIsClassified(t *testing.T) {
	shop := openInternalShop(t)
	ctx := context.Background()
	now := internalTime("2026-11-27", 10, 0)
	var (
		inbox   dbgen.AiInbox
		session dbgen.AiSession
		subject Subject
	)
	err := db.InTenant(ctx, shop.pool, shop.res.TenantID, func(q *dbgen.Queries) error {
		var err error
		inbox, err = EnsureInbox(ctx, q, shop.res.StoreID, nil)
		if err != nil {
			return err
		}
		key := uuid.NewString()
		if _, err := q.CreateRedemption(ctx, dbgen.CreateRedemptionParams{
			TenantID: shop.res.TenantID, StoreID: shop.res.StoreID, Platform: "douyin",
			CouponCode: key, IdempotencyKey: key, RedeemedBy: shop.res.OwnerID,
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			return err
		}
		row, err := q.GetRedemption(ctx, dbgen.GetRedemptionParams{StoreID: shop.res.StoreID, IdempotencyKey: key})
		if err != nil {
			return err
		}
		if _, err := q.UpdateRedemption(ctx, dbgen.UpdateRedemptionParams{
			StoreID: shop.res.StoreID, IdempotencyKey: key, Status: "redeemed",
			RedeemedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}); err != nil {
			return err
		}
		subject = Subject{Kind: SubjectCouponRedemption, ID: row.ID}
		if _, err := CreateRequest(ctx, q, now, CreateRequestInput{
			StoreID: shop.res.StoreID, ToStaffID: shop.res.OwnerID, Kind: "inform", Subject: subject,
		}); err != nil {
			return err
		}
		session, err = q.CreateSession(ctx, dbgen.CreateSessionParams{
			TenantID: shop.res.TenantID, StoreID: shop.res.StoreID, InboxID: inbox.ID, Now: timestamp(now),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	invocations := map[string]struct {
		storeRun bool
		args     string
	}{
		"remind_me": {args: fmt.Sprintf(`{"tier":"now","subject_kind":"%s","subject_id":"%s","body":"这张券还没关联单据"}`,
			SubjectCouponRedemption, subject.ID)},
		"schedule_follow_up": {storeRun: true, args: fmt.Sprintf(`{"subject_kind":"%s","subject_id":"%s","minutes":120}`,
			SubjectCouponRedemption, subject.ID)},
	}
	for tool := range classifierTools {
		if _, ok := invocations[tool]; !ok {
			t.Fatalf("classifierTools 新增了 %q，但测试没有合法调用；请补一条并确认 handler 调用了 classifyTool", tool)
		}
	}

	for tool, invocation := range invocations {
		t.Run(tool, func(t *testing.T) {
			rc := &runContext{
				runner:   &Runner{Classifier: func() ai.Model { return alwaysBlockClassifier{} }},
				storeRun: invocation.storeRun,
				caller: ops.Caller{
					TenantID: shop.res.TenantID, StoreID: shop.res.StoreID, StaffID: shop.res.OwnerID,
				},
				in: RunInput{
					TenantID: shop.res.TenantID, StoreID: shop.res.StoreID, Inbox: inbox, Session: session, Now: now,
					Tx: func(ctx context.Context, fn func(*dbgen.Queries) error) error {
						return db.InTenant(ctx, shop.pool, shop.res.TenantID, fn)
					},
				},
			}
			_, err := rc.execute(ctx, tool, invocation.args)
			if err == nil || !strings.Contains(err.Error(), "分类器没放行") {
				t.Fatalf("%s = %v, want classifier block", tool, err)
			}
			if err := db.InTenant(ctx, shop.pool, shop.res.TenantID, func(q *dbgen.Queries) error {
				timers, err := q.ListTimers(ctx, inbox.ID)
				if err != nil {
					return err
				}
				if len(timers) != 0 {
					return fmt.Errorf("timers = %+v", timers)
				}
				outbox, err := q.ListOutboxForRecipient(ctx, dbgen.ListOutboxForRecipientParams{
					StoreID: shop.res.StoreID, RecipientID: shop.res.OwnerID,
				})
				if err != nil {
					return err
				}
				if len(outbox) != 0 {
					return fmt.Errorf("outbox = %+v", outbox)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}

	tx := dbtest.AppTx(t, shop.pool, shop.res.TenantID)
	var checks int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_classifier_checks WHERE store_id = $1`, shop.res.StoreID).Scan(&checks); err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	dbtest.Rollback(t, tx)
	if checks != len(invocations) {
		t.Fatalf("classifier checks = %d, want %d", checks, len(invocations))
	}
}

func TestClassifierArgsRuleMessage(t *testing.T) {
	if _, err := classifierArgs(json.RawMessage(`[]`)); err == nil || err.Error() != "工具参数不是 JSON 对象" {
		t.Fatalf("classifierArgs = %v", err)
	}
}
