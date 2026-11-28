package storeai_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/dbtest"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/optest"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
)

type countingClassifier struct {
	mu       sync.Mutex
	calls    int
	reply    string
	err      error
	messages [][]ai.Message
}

func (m *countingClassifier) Chat(_ context.Context, messages []ai.Message, _ []ops.Tool) (ai.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.messages = append(m.messages, append([]ai.Message(nil), messages...))
	if m.err != nil {
		return ai.Message{}, m.err
	}
	reply := m.reply
	if reply == "" {
		reply = `{"decision":"allow"}`
	}
	return ai.Message{Role: ai.RoleAssistant, Content: reply}, nil
}

func (m *countingClassifier) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *countingClassifier) LastUserContent() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) == 0 {
		return ""
	}
	messages := m.messages[len(m.messages)-1]
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == ai.RoleUser {
			return messages[i].Content
		}
	}
	return ""
}

type classifierCheckRow struct {
	Tool       string
	Decision   string
	Source     string
	Reason     *string
	ModelReply *string
}

func classifierChecks(t *testing.T, shop *optest.Shop) []classifierCheckRow {
	t.Helper()
	tx := dbtest.AppTx(t, shop.Pool, shop.Res.TenantID)
	rows, err := tx.Query(context.Background(), `
		SELECT tool, decision, source, reason, model_reply
		FROM ai_classifier_checks WHERE store_id = $1 ORDER BY created_at, id`, shop.Res.StoreID)
	if err != nil {
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	var out []classifierCheckRow
	for rows.Next() {
		var row classifierCheckRow
		if err := rows.Scan(&row.Tool, &row.Decision, &row.Source, &row.Reason, &row.ModelReply); err != nil {
			rows.Close()
			dbtest.Rollback(t, tx)
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		dbtest.Rollback(t, tx)
		t.Fatal(err)
	}
	rows.Close()
	dbtest.Rollback(t, tx)
	return out
}

func classifyOnce(t *testing.T, shop *optest.Shop, model ai.Model, in storeai.ClassifyInput) (storeai.ClassifyResult, error) {
	t.Helper()
	return storeai.Classify(context.Background(), func(ctx context.Context, fn func(*dbgen.Queries) error) error {
		return db.InTenant(ctx, shop.Pool, shop.Res.TenantID, fn)
	}, model, in)
}

func classifierInput(shop *optest.Shop, now time.Time, inbox dbgen.AiInbox) storeai.ClassifyInput {
	return storeai.ClassifyInput{
		TenantID: shop.Res.TenantID, StoreID: shop.Res.StoreID, InboxID: inbox.ID, Now: now,
	}
}

func remindArgs(subject uuid.UUID, body string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "body": body,
	})
	return raw
}

func TestClassifierPhoneRule(t *testing.T) {
	tests := []struct {
		body  string
		block bool
	}{
		{"打 13800000000 问一下", true},
		{"电话 138 0000 0000", true},
		{"电话 138-0000-0000", true},
		{"+8613800000000", true},
		{"0086 138-0000-0000", true},
		{"+86 138 0000 0000", true},
		{"单号 13800000001 还没关联", true},
		{"单号 138000000001 还没关联", false},
	}
	for _, tc := range tests {
		t.Run(strings.ReplaceAll(tc.body, " ", "_"), func(t *testing.T) {
			shop := open(t)
			now := at("2026-11-27", 10, 0)
			subject := insertCouponRedemption(t, shop, now)
			inbox := storeInbox(t, shop)
			model := &countingClassifier{}
			in := classifierInput(shop, now, inbox)
			in.Tool = "remind_me"
			in.RecipientID = shop.Res.OwnerID
			in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
			in.Args = remindArgs(subject, tc.body)
			result, err := classifyOnce(t, shop, model, in)
			if err != nil {
				t.Fatal(err)
			}
			if tc.block {
				if result.Decision != storeai.ClassifierBlock || result.Source != storeai.ClassifierRule || result.Reason != "提醒里不能写手机号" {
					t.Fatalf("result = %+v", result)
				}
				if model.Count() != 0 {
					t.Fatalf("model calls = %d", model.Count())
				}
			} else if result.Decision != storeai.ClassifierAllow || result.Source != storeai.ClassifierModel || model.Count() != 1 {
				t.Fatalf("result = %+v calls=%d", result, model.Count())
			}
		})
	}
}

func TestClassifierRecipientRule(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	inactive, _ := shop.Hire("停用员工", "099", "inactive", shop.Role("发型师"))
	shop.Call(t, shop.Owner, "deactivate_staff", map[string]any{"staff_id": inactive}, 200)
	for _, recipient := range []uuid.UUID{uuid.New(), inactive} {
		model := &countingClassifier{}
		in := classifierInput(shop, now, inbox)
		in.Tool = "remind_me"
		in.RecipientID = recipient
		in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
		in.Args = remindArgs(subject, "这张券还没关联单据")
		result, err := classifyOnce(t, shop, model, in)
		if err != nil {
			t.Fatal(err)
		}
		if result.Decision != storeai.ClassifierBlock || result.Source != storeai.ClassifierRule || result.Reason != "收件人不是本店在职员工" {
			t.Fatalf("recipient %s: %+v", recipient, result)
		}
		if model.Count() != 0 {
			t.Fatalf("model calls = %d", model.Count())
		}
	}
}

func TestClassifierDuplicateRule(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	staff := shop.Res.OwnerID
	insertOutbox(t, shop, staff, "now", storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}, "这张券还没关联单据", "sent", nil, &now, nil, nil)
	model := &countingClassifier{}
	in := classifierInput(shop, now, inbox)
	in.Tool = "remind_me"
	in.RecipientID = staff
	in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
	in.Args = remindArgs(subject, "这张券还没关联单据")
	result, err := classifyOnce(t, shop, model, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != storeai.ClassifierBlock || result.Source != storeai.ClassifierRule || result.Reason != "已经提醒过同样的内容" || model.Count() != 0 {
		t.Fatalf("result = %+v calls=%d", result, model.Count())
	}
}

func TestClassifierDuplicateWithdrawnAllowed(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	staff := shop.Res.OwnerID
	insertOutbox(t, shop, staff, "now", storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}, "这张券还没关联单据", "withdrawn", nil, nil, nil, &now)
	model := &countingClassifier{}
	in := classifierInput(shop, now, inbox)
	in.Tool = "remind_me"
	in.RecipientID = staff
	in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
	in.Args = remindArgs(subject, "这张券还没关联单据")
	result, err := classifyOnce(t, shop, model, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != storeai.ClassifierAllow || result.Source != storeai.ClassifierModel || model.Count() != 1 {
		t.Fatalf("result = %+v calls=%d", result, model.Count())
	}
}

func TestClassifierFollowUpRule(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	until := now.Add(30 * time.Minute)
	facts := &storeai.ClassifyFacts{Platform: "douyin", RedeemedAt: &now, RevocableUntil: &until}
	for _, tc := range []struct {
		minutes int
		block   bool
	}{
		{45, true},
		{30, false},
		{20, false},
	} {
		due := now.Add(time.Duration(tc.minutes) * time.Minute)
		args, _ := json.Marshal(map[string]any{
			"subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "minutes": tc.minutes,
		})
		model := &countingClassifier{}
		in := classifierInput(shop, now, inbox)
		in.Tool = "schedule_follow_up"
		in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
		in.Args = args
		in.DueAt = &due
		in.Facts = facts
		result, err := classifyOnce(t, shop, model, in)
		if err != nil {
			t.Fatal(err)
		}
		if tc.block {
			if result.Decision != storeai.ClassifierBlock || result.Source != storeai.ClassifierRule || result.Reason != "跟进时间晚于撤销时限" {
				t.Fatalf("minutes %d: %+v", tc.minutes, result)
			}
		} else if result.Decision != storeai.ClassifierAllow || result.Source != storeai.ClassifierRule {
			t.Fatalf("minutes %d: %+v", tc.minutes, result)
		}
		if model.Count() != 0 {
			t.Fatalf("minutes %d model calls = %d", tc.minutes, model.Count())
		}
	}
}

func TestClassifierMasksExistingReminderPhones(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	staff := shop.Res.OwnerID
	insertOutbox(t, shop, staff, "now", storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}, "打 +8613800000000 问一下", "sent", nil, &now, nil, nil)
	insertOutbox(t, shop, staff, "now", storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}, "撤回的 13800000001", "withdrawn", nil, nil, nil, &now)
	model := &countingClassifier{}
	in := classifierInput(shop, now, inbox)
	in.Tool = "remind_me"
	in.RecipientID = staff
	in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
	in.Args = remindArgs(subject, "这张券还没关联单据")
	result, err := classifyOnce(t, shop, model, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != storeai.ClassifierAllow || model.Count() != 1 {
		t.Fatalf("result = %+v calls=%d", result, model.Count())
	}
	user := model.LastUserContent()
	if strings.Contains(user, "13800000000") || strings.Contains(user, "13800000001") {
		t.Fatalf("模型 user 消息含手机号：%s", user)
	}
	if !strings.Contains(user, "[手机号]") {
		t.Fatalf("模型 user 消息没有脱敏占位符：%s", user)
	}
	if strings.Contains(user, "撤回的") {
		t.Fatalf("模型 user 消息含已撤回提醒：%s", user)
	}
}

func TestClassifierModelAllowRecordsCheck(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	model := &countingClassifier{}
	in := classifierInput(shop, now, inbox)
	in.Tool = "remind_me"
	in.RecipientID = shop.Res.OwnerID
	in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
	in.Args = remindArgs(subject, "这张券还没关联单据")
	result, err := classifyOnce(t, shop, model, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != storeai.ClassifierAllow || result.Source != storeai.ClassifierModel {
		t.Fatalf("result = %+v", result)
	}
	checks := classifierChecks(t, shop)
	if len(checks) != 1 || checks[0].Decision != "allow" || checks[0].Source != "model" || checks[0].ModelReply == nil {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestClassifierParseFailureBlocks(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	model := &countingClassifier{reply: "我不知道"}
	in := classifierInput(shop, now, inbox)
	in.Tool = "remind_me"
	in.RecipientID = shop.Res.OwnerID
	in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
	in.Args = remindArgs(subject, "这张券还没关联单据")
	result, err := classifyOnce(t, shop, model, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != storeai.ClassifierBlock || result.Source != storeai.ClassifierModel || result.Reason != "分类器没有给出可用结论" {
		t.Fatalf("result = %+v", result)
	}
	if result.ModelReply == nil || *result.ModelReply != "我不知道" {
		t.Fatalf("model reply = %v", result.ModelReply)
	}
	checks := classifierChecks(t, shop)
	if len(checks) != 1 || checks[0].Decision != "block" || checks[0].Source != "model" || checks[0].ModelReply == nil || *checks[0].ModelReply != "我不知道" {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestClassifierModelErrorDoesNotRecord(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	model := &countingClassifier{err: errors.New("classifier down")}
	in := classifierInput(shop, now, inbox)
	in.Tool = "remind_me"
	in.RecipientID = shop.Res.OwnerID
	in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
	in.Args = remindArgs(subject, "这张券还没关联单据")
	if _, err := classifyOnce(t, shop, model, in); err == nil || ops.StatusOf(err) < 500 {
		t.Fatalf("err = %v", err)
	}
	if checks := classifierChecks(t, shop); len(checks) != 0 {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestClassifierPrecheckSkipsClassifier(t *testing.T) {
	t.Run("schedule_follow_up subject missing", func(t *testing.T) {
		shop := open(t)
		now := at("2026-11-27", 10, 0)
		missing := uuid.New()
		emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+missing.String(), missing, "{}")
		main := &scriptedModel{turns: []ai.Message{
			toolTurn("schedule_follow_up", map[string]any{"subject_kind": storeai.SubjectCouponRedemption, "subject_id": missing, "minutes": 30}),
			finalTurn("结束"),
		}}
		classifier := &countingClassifier{}
		runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Classifier: func() ai.Model { return classifier }, Logger: discardLogger()}
		runOnce(t, shop, now, runner.Run)
		if classifier.Count() != 0 {
			t.Fatalf("classifier calls = %d", classifier.Count())
		}
		if checks := classifierChecks(t, shop); len(checks) != 0 {
			t.Fatalf("checks = %+v", checks)
		}
		if timers := allTimers(t, shop, storeInbox(t, shop).ID); len(timers) != 0 {
			t.Fatalf("timers = %+v", timers)
		}
		final := main.messages[len(main.messages)-1]
		if !strings.Contains(final[len(final)-1].Content, "对象不在本店") {
			t.Fatalf("tool result = %+v", final)
		}
	})
	t.Run("remind_me without open request", func(t *testing.T) {
		shop := open(t)
		now := at("2026-11-27", 10, 0)
		subject := insertCouponRedemption(t, shop, now)
		staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
		emitEvent(t, shop, staffInbox(t, shop, staff), now, storeai.EventPeriodicDue, "periodic:test", storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}, "{}")
		main := &scriptedModel{turns: []ai.Message{
			toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "body": "这张券还没关联单据"}),
			finalTurn("结束"),
		}}
		classifier := &countingClassifier{}
		runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Classifier: func() ai.Model { return classifier }, Logger: discardLogger()}
		runOnce(t, shop, now, runner.Run)
		if classifier.Count() != 0 {
			t.Fatalf("classifier calls = %d", classifier.Count())
		}
		if checks := classifierChecks(t, shop); len(checks) != 0 {
			t.Fatalf("checks = %+v", checks)
		}
		if rows := outboxRows(t, shop, staff); len(rows) != 0 {
			t.Fatalf("outbox = %+v", rows)
		}
		final := main.messages[len(main.messages)-1]
		if !strings.Contains(final[len(final)-1].Content, "这个对象没有交给本人的待办") {
			t.Fatalf("tool result = %+v", final)
		}
	})
}

func TestClassifiedToolsNeedClassifier(t *testing.T) {
	t.Run("remind_me", func(t *testing.T) {
		shop := open(t)
		now := at("2026-11-27", 10, 0)
		subject := insertCouponRedemption(t, shop, now)
		staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
		createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}})
		main := &scriptedModel{turns: []ai.Message{
			toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "body": "这张券还没关联单据"}),
			finalTurn("已提醒"),
		}}
		classifier := &countingClassifier{}
		runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Classifier: func() ai.Model { return classifier }, Logger: discardLogger()}
		runOnce(t, shop, now, runner.Run)
		if rows := outboxRows(t, shop, staff); len(rows) != 1 {
			t.Fatalf("outbox = %+v", rows)
		}
		if classifier.Count() != 1 {
			t.Fatalf("classifier calls = %d", classifier.Count())
		}
		checks := classifierChecks(t, shop)
		if len(checks) != 1 || checks[0].Tool != "remind_me" || checks[0].Decision != "allow" || checks[0].Source != "model" {
			t.Fatalf("checks = %+v", checks)
		}
	})
	t.Run("schedule_follow_up", func(t *testing.T) {
		shop := open(t)
		now := at("2026-11-27", 10, 0)
		subject := insertCouponRedemption(t, shop, now)
		emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+subject.String(), subject, "{}")
		main := &scriptedModel{turns: []ai.Message{
			toolTurn("schedule_follow_up", map[string]any{"subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "minutes": 30}),
			finalTurn("定好跟进"),
		}}
		classifier := &countingClassifier{}
		runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Classifier: func() ai.Model { return classifier }, Logger: discardLogger()}
		runOnce(t, shop, now, runner.Run)
		if timers := allTimers(t, shop, storeInbox(t, shop).ID); len(timers) != 1 {
			t.Fatalf("timers = %+v", timers)
		}
		if classifier.Count() != 0 {
			t.Fatalf("schedule_follow_up should not call the classifier model: %d", classifier.Count())
		}
		checks := classifierChecks(t, shop)
		if len(checks) != 1 || checks[0].Tool != "schedule_follow_up" || checks[0].Decision != "allow" || checks[0].Source != "rule" {
			t.Fatalf("checks = %+v", checks)
		}
	})
}

func TestUnclassifiedToolsSkipClassifier(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	emitCouponEventForTest(t, shop, now, storeai.EventCouponLinked, "coupon_linked:"+subject.String(), subject, "{}")
	main := &scriptedModel{turns: []ai.Message{toolTurn("finish", map[string]any{"status": "closed"}), finalTurn("结束")}}
	classifier := &countingClassifier{}
	runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Classifier: func() ai.Model { return classifier }, Logger: discardLogger()}
	runOnce(t, shop, now, runner.Run)
	if classifier.Count() != 0 {
		t.Fatalf("classifier calls = %d", classifier.Count())
	}
	if checks := classifierChecks(t, shop); len(checks) != 0 {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestClassifierBlockDoesNotWrite(t *testing.T) {
	t.Run("remind_me phone", func(t *testing.T) {
		shop := open(t)
		now := at("2026-11-27", 10, 0)
		subject := insertCouponRedemption(t, shop, now)
		staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
		createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}})
		main := &scriptedModel{turns: []ai.Message{
			toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "body": "打 13800000000"}),
			finalTurn("结束"),
		}}
		classifier := &countingClassifier{}
		runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Classifier: func() ai.Model { return classifier }, Logger: discardLogger()}
		runOnce(t, shop, now, runner.Run)
		if rows := outboxRows(t, shop, staff); len(rows) != 0 {
			t.Fatalf("outbox = %+v", rows)
		}
		if classifier.Count() != 0 {
			t.Fatalf("classifier model calls = %d", classifier.Count())
		}
		final := main.messages[len(main.messages)-1]
		if !strings.Contains(final[len(final)-1].Content, "分类器没放行：提醒里不能写手机号。换一种做法，或者不发。") {
			t.Fatalf("tool result = %+v", final)
		}
		if checks := classifierChecks(t, shop); len(checks) != 1 || checks[0].Decision != "block" {
			t.Fatalf("checks = %+v", checks)
		}
	})
	t.Run("schedule_follow_up past revocable", func(t *testing.T) {
		shop := open(t)
		now := at("2026-11-27", 10, 0)
		subject := insertCouponRedemption(t, shop, now)
		emitCouponEventForTest(t, shop, now, storeai.EventCouponRedeemed, "coupon_redeemed:"+subject.String(), subject, "{}")
		main := &scriptedModel{turns: []ai.Message{
			toolTurn("schedule_follow_up", map[string]any{"subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "minutes": 90}),
			finalTurn("结束"),
		}}
		runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Logger: discardLogger()}
		runOnce(t, shop, now, runner.Run)
		if timers := allTimers(t, shop, storeInbox(t, shop).ID); len(timers) != 0 {
			t.Fatalf("timers = %+v", timers)
		}
		checks := classifierChecks(t, shop)
		if len(checks) != 1 || checks[0].Decision != "block" || checks[0].Source != "rule" || checks[0].Reason == nil || *checks[0].Reason != "跟进时间晚于撤销时限" {
			t.Fatalf("checks = %+v", checks)
		}
	})
}

func TestClassifierModelErrorRetriesEvent(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}})
	main := &scriptedModel{turns: []ai.Message{
		toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "body": "这张券还没关联单据"}),
		finalTurn("不应到这里"),
	}}
	classifier := &countingClassifier{err: ai.ErrNotConfigured}
	runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Classifier: func() ai.Model { return classifier }, Logger: discardLogger()}
	runOnce(t, shop, now, runner.Run)
	events := allEvents(t, shop, staffInbox(t, shop, staff).ID)
	if len(events) != 1 || events[0].Status != "pending" || events[0].Attempts != 1 {
		t.Fatalf("events = %+v", events)
	}
	if rows := outboxRows(t, shop, staff); len(rows) != 0 {
		t.Fatalf("outbox = %+v", rows)
	}
	if checks := classifierChecks(t, shop); len(checks) != 0 {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestClassifierRuleDoesNotCallModel(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	staff := shop.Res.OwnerID
	insertOutbox(t, shop, staff, "now", storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}, "这张券还没关联单据", "sent", nil, &now, nil, nil)
	model := &countingClassifier{}
	in := classifierInput(shop, now, inbox)
	in.Tool = "remind_me"
	in.RecipientID = staff
	in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
	in.Args = remindArgs(subject, "这张券还没关联单据")
	result, err := classifyOnce(t, shop, model, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != storeai.ClassifierBlock || result.Source != storeai.ClassifierRule || model.Count() != 0 {
		t.Fatalf("result = %+v calls=%d", result, model.Count())
	}
}

func TestClassifierRuleRecordsBlock(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	inbox := storeInbox(t, shop)
	staff := shop.Res.OwnerID
	insertOutbox(t, shop, staff, "now", storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}, "这张券还没关联单据", "sent", nil, &now, nil, nil)
	model := &countingClassifier{}
	in := classifierInput(shop, now, inbox)
	in.Tool = "remind_me"
	in.RecipientID = staff
	in.Subject = storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}
	in.Args = remindArgs(subject, "这张券还没关联单据")
	if _, err := classifyOnce(t, shop, model, in); err != nil {
		t.Fatal(err)
	}
	checks := classifierChecks(t, shop)
	if len(checks) != 1 || checks[0].Decision != "block" || checks[0].Source != "rule" || checks[0].Reason == nil || *checks[0].Reason != "已经提醒过同样的内容" {
		t.Fatalf("checks = %+v", checks)
	}
}

type cancelOnClassify struct {
	shop      *optest.Shop
	requestID uuid.UUID
}

func (m cancelOnClassify) Chat(ctx context.Context, _ []ai.Message, _ []ops.Tool) (ai.Message, error) {
	if err := db.InTenant(ctx, m.shop.Pool, m.shop.Res.TenantID, func(q *dbgen.Queries) error {
		_, err := storeai.CancelRequest(ctx, q, m.requestID)
		return err
	}); err != nil {
		return ai.Message{}, err
	}
	return ai.Message{Role: ai.RoleAssistant, Content: `{"decision":"allow"}`}, nil
}

// TestRemindMeRechecksOpenRequestInWriteTx 让分类器在放行前取消交办；
// 写事务必须再查一次，否则会写出一条没有待办的提醒。
func TestRemindMeRechecksOpenRequestInWriteTx(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	request := createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}})
	main := &scriptedModel{turns: []ai.Message{
		toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "body": "这张券还没关联单据"}),
		finalTurn("结束"),
	}}
	runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return main }, Classifier: func() ai.Model { return cancelOnClassify{shop: shop, requestID: request.ID} }, Logger: discardLogger()}
	runOnce(t, shop, now, runner.Run)
	if rows := outboxRows(t, shop, staff); len(rows) != 0 {
		t.Fatalf("outbox = %+v", rows)
	}
	final := main.messages[len(main.messages)-1]
	if !strings.Contains(final[len(final)-1].Content, "这个对象没有交给本人的待办") {
		t.Fatalf("tool result = %+v", final)
	}
}

func TestClassifierFallsBackToRunnerModel(t *testing.T) {
	shop := open(t)
	now := at("2026-11-27", 10, 0)
	subject := insertCouponRedemption(t, shop, now)
	staff, _ := shop.Hire("小李", "002", "li01", shop.Role("发型师"))
	createRequest(t, shop, now, storeai.CreateRequestInput{StoreID: shop.Res.StoreID, ToStaffID: staff, Kind: "inform", Subject: storeai.Subject{Kind: storeai.SubjectCouponRedemption, ID: subject}})
	model := &fallbackModel{agent: &scriptedModel{turns: []ai.Message{
		toolTurn("remind_me", map[string]any{"tier": "now", "subject_kind": storeai.SubjectCouponRedemption, "subject_id": subject, "body": "这张券还没关联单据"}),
		finalTurn("已提醒"),
	}}}
	runner := &storeai.Runner{Registry: shop.Registry, Model: func() ai.Model { return model }, Logger: discardLogger()}
	runOnce(t, shop, now, runner.Run)
	if rows := outboxRows(t, shop, staff); len(rows) != 1 {
		t.Fatalf("outbox = %+v", rows)
	}
}

type fallbackModel struct {
	agent *scriptedModel
}

func (m *fallbackModel) Chat(ctx context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	if len(tools) == 0 {
		return ai.Message{Role: ai.RoleAssistant, Content: `{"decision":"allow"}`}, nil
	}
	return m.agent.Chat(ctx, messages, tools)
}
