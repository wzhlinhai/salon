package eval

import (
	"context"
	"encoding/json"
	"flag"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/drafts"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

var (
	updateCassette = flag.Bool("update-cassette", false, "用 oracle 重写 testdata/baseline.cassette.json")
	baselinePath   = "testdata/baseline.cassette.json"
)

func mustSuite(t *testing.T) *Suite {
	t.Helper()
	suite, err := LoadSuite()
	if err != nil {
		t.Fatal(err)
	}
	return suite
}

func TestEvalCasesValid(t *testing.T) {
	suite := mustSuite(t)
	if len(suite.Cases) != 40 {
		t.Fatalf("cases = %d, want 40", len(suite.Cases))
	}
	seen := map[string]bool{}
	for _, c := range suite.Cases {
		if seen[c.ID] {
			t.Fatalf("duplicate case id %s", c.ID)
		}
		seen[c.ID] = true
		if c.Story == "" || c.Category == "" || len(c.Messages) == 0 || len(c.AnswerContains) == 0 || c.ReferenceAnswer == "" {
			t.Fatalf("incomplete case %+v", c)
		}
	}
}

func TestEvalOracleScoresPerfect(t *testing.T) {
	suite := mustSuite(t)
	recorder := NewRecorder()
	factory := func(c Case, f *Fixture) ai.Model { return recorder.Model(c, f, NewOracle(c, f)) }
	report, err := Run(context.Background(), t, suite, RunConfig{ModelName: "oracle", BaseURLHost: "offline"}, factory)
	if err != nil {
		t.Fatal(err)
	}
	assertPerfect(t, report, len(suite.Cases))
	for _, result := range report.Cases {
		for _, source := range result.Sources {
			if !source.OK {
				t.Fatalf("%s: source %s failed: %s", result.ID, source.Tool, source.Error)
			}
		}
	}
	cassette := recorder.Cassette(suite.Hash)
	if *updateCassette {
		if err := SaveCassette(baselinePath, cassette); err != nil {
			t.Fatal(err)
		}
		return
	}
	committed, err := LoadCassette(baselinePath)
	if err != nil {
		t.Fatalf("load baseline cassette: %v (run go test ./internal/ai/eval -run TestEvalOracleScoresPerfect -update-cassette)", err)
	}
	if !reflect.DeepEqual(comparableCassette(committed), comparableCassette(cassette)) {
		t.Fatalf("baseline cassette is stale: run go test ./internal/ai/eval -run TestEvalOracleScoresPerfect -update-cassette")
	}
}

func TestEvalReplayMatchesOracle(t *testing.T) {
	suite := mustSuite(t)
	cassette, err := LoadCassette(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	replay := NewReplay(cassette)
	factory := func(c Case, f *Fixture) ai.Model { return replay.Model(c, f) }
	report, err := Run(context.Background(), t, suite, RunConfig{ModelName: "replay", BaseURLHost: "offline"}, factory)
	if err != nil {
		t.Fatal(err)
	}
	assertPerfect(t, report, len(suite.Cases))
	if report.Run.CaseFileHash != cassette.CaseFileHash {
		t.Fatalf("case hash = %s, cassette = %s", report.Run.CaseFileHash, cassette.CaseFileHash)
	}
}

func assertPerfect(t *testing.T, report Report, cases int) {
	t.Helper()
	for _, result := range report.Cases {
		if !result.Overall || !result.ToolSelection || !result.Arguments || !result.Answer || !result.ToolsOK {
			t.Errorf("%s not perfect: tool=%v args=%v answer=%v toolsOK=%v reasons=%v sources=%+v reply=%q", result.ID, result.ToolSelection, result.Arguments, result.Answer, result.ToolsOK, result.FailureReasons, result.Sources, result.Reply)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	aggregate := report.Aggregates.Overall
	if aggregate.Cases != cases || aggregate.ToolSelectionAccuracy != 1 || aggregate.ArgumentCallAccuracy != 1 || aggregate.ArgumentCaseAccuracy != 1 || aggregate.AnswerAccuracy != 1 || aggregate.ToolSuccessAccuracy != 1 || aggregate.OverallAccuracy != 1 {
		t.Fatalf("aggregate = %+v", aggregate)
	}
}

func TestEvalScorerFailures(t *testing.T) {
	base := Case{ID: "x", Story: "s", Category: "c", Role: "owner", Messages: []CaseMessage{{Role: "user", Content: "问"}}, AnswerContains: []string{"OK"}, ReferenceAnswer: "OK"}
	for _, tc := range []struct {
		name     string
		mutate   func(*Case, *Observed)
		modelErr error
		want     string
	}{
		{"missing tool", func(c *Case, o *Observed) { c.Expected = []ExpectedCall{{Tool: "get_me"}}; o.Sources = nil }, nil, "缺少工具调用"},
		{"wrong order", func(c *Case, o *Observed) {
			c.Expected = []ExpectedCall{{Tool: "get_me"}, {Tool: "list_performance"}}
			o.Sources = []ai.Source{{Tool: "list_performance", OK: true}, {Tool: "get_me", OK: true}}
		}, nil, "工具顺序不符"},
		{"forbidden", func(c *Case, o *Observed) {
			c.Expected = []ExpectedCall{{Tool: "get_me"}}
			c.Forbidden = []string{"redeem_coupon"}
			o.Sources = []ai.Source{{Tool: "get_me", OK: true}, {Tool: "redeem_coupon", OK: false}}
		}, nil, "出现禁止的工具"},
		{"wrong arg", func(c *Case, o *Observed) {
			c.Expected = []ExpectedCall{{Tool: "get_me", Args: map[string]any{"id": "a"}}}
			o.Sources = []ai.Source{{Tool: "get_me", OK: true, Arguments: map[string]any{"id": "b"}}}
		}, nil, "参数不匹配"},
		{"tool error", func(c *Case, o *Observed) {
			c.Expected = []ExpectedCall{{Tool: "get_me"}}
			o.Sources = []ai.Source{{Tool: "get_me", OK: false, Error: "permission denied"}}
		}, nil, "工具执行失败"},
		{"answer missing", func(c *Case, o *Observed) { o.Reply = "不同" }, nil, "回答缺少关键词"},
		{"model error", func(c *Case, o *Observed) { o.Reply = "" }, ai.ErrUnavailable, "模型不可用"},
		{"model http error", func(c *Case, o *Observed) { o.Reply = "" }, &ai.ModelError{Status: 500}, "模型错误：HTTP 500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, observed := base, Observed{Reply: "OK"}
			tc.mutate(&c, &observed)
			result := ScoreCase(c, observed, tc.modelErr)
			if result.Overall {
				t.Fatalf("overall should fail: %+v", result)
			}
			if !strings.Contains(strings.Join(result.FailureReasons, "\n"), tc.want) {
				t.Fatalf("reasons = %v, want %q", result.FailureReasons, tc.want)
			}
		})
	}
}

func TestEvalScorerAnyOfAndDraftArgument(t *testing.T) {
	anyOf := Case{ID: "any", Story: "s", Category: "c", Role: "owner", Messages: []CaseMessage{{Role: "user", Content: "问"}}, AnswerContains: []string{"OK"}, ReferenceAnswer: "OK", Expected: []ExpectedCall{{AnyOf: []string{"get_me", "list_performance"}}}}
	if result := ScoreCase(anyOf, Observed{Reply: "OK", Sources: []ai.Source{{Tool: "list_performance", OK: true}}}, nil); !result.Overall {
		t.Fatalf("any_of should pass: %+v", result)
	}
	draftID := uuid.New()
	draftCase := Case{ID: "draft", Story: "s", Category: "c", Role: "owner", Messages: []CaseMessage{{Role: "user", Content: "问"}}, AnswerContains: []string{"OK"}, ReferenceAnswer: "OK", Expected: []ExpectedCall{{Tool: "update_draft_params", Args: map[string]any{"id": "$draft", "params": map[string]any{"gender": "male"}}}}}
	drafts := []ai.DraftView{{Draft: drafts.Draft{ID: draftID, Operation: "open_order"}}}
	sources := []ai.Source{{Tool: "update_draft_params", OK: true, Arguments: map[string]any{"id": draftID.String(), "params": map[string]any{"gender": "male", "customer_id": "c"}}}}
	if result := ScoreCase(draftCase, Observed{Reply: "OK", Sources: sources, Drafts: drafts}, nil); !result.Overall {
		t.Fatalf("draft arg should pass: %+v", result)
	}
	if result := ScoreCase(draftCase, Observed{Reply: "OK", Sources: sources}, nil); result.Overall {
		t.Fatal("draft arg should fail without a draft")
	}
}

func TestEvalCompare(t *testing.T) {
	oldReport := Report{Run: RunMeta{CaseFileHash: "same"}, Cases: []CaseResult{
		{ID: "a", Story: "read", Overall: true},
		{ID: "b", Story: "read", Overall: false},
	}, Aggregates: Aggregates{Overall: Metrics{ToolSelectionAccuracy: 0.5, OverallAccuracy: 0.5}, ByStory: map[string]Metrics{"read": {ToolSelectionAccuracy: 0.5}}}}
	newReport := Report{Run: RunMeta{CaseFileHash: "same"}, Cases: []CaseResult{
		{ID: "a", Story: "read", Overall: false},
		{ID: "b", Story: "read", Overall: true},
	}, Aggregates: Aggregates{Overall: Metrics{ToolSelectionAccuracy: 1, OverallAccuracy: 0.5}, ByStory: map[string]Metrics{"read": {ToolSelectionAccuracy: 1}}}}
	comparison, err := Compare(oldReport, newReport, false)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Metrics[0].Delta != 0.5 {
		t.Fatalf("delta = %+v", comparison.Metrics[0])
	}
	if len(comparison.Flips) != 2 || comparison.Flips[0].ID != "a" || comparison.Flips[0].Before != "通过" || comparison.Flips[0].After != "未通过" || comparison.Flips[1].ID != "b" || comparison.Flips[1].Before != "未通过" || comparison.Flips[1].After != "通过" {
		t.Fatalf("flips = %+v", comparison.Flips)
	}
	different := newReport
	different.Run.CaseFileHash = "other"
	if _, err := Compare(oldReport, different, false); err == nil || !strings.Contains(err.Error(), "用例文件哈希不同") {
		t.Fatalf("err = %v", err)
	}
	if _, err := Compare(oldReport, different, true); err != nil {
		t.Fatal(err)
	}
}

func TestEvalReplayMismatch(t *testing.T) {
	recording := CaseRecording{PromptHash: "prompt", Fingerprint: "fixture", Calls: []RecordedCall{{
		Messages: 2, Tools: []string{"get_me"}, System: "sys", Users: []string{"u"}, Response: &ai.Message{Role: ai.RoleAssistant, Content: "ok"},
	}}}
	model := newReplayModel("x", recording, nil, "prompt", "fixture")
	if _, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser}, {Role: ai.RoleUser}, {Role: ai.RoleUser}}, nil); err == nil || !strings.Contains(err.Error(), "消息数") {
		t.Fatalf("messages mismatch err = %v", err)
	}
	model = newReplayModel("x", recording, nil, "prompt", "fixture")
	if _, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleUser}, {Role: ai.RoleUser}}, []ops.Tool{{Name: "other"}}); err == nil || !strings.Contains(err.Error(), "工具列表") {
		t.Fatalf("tools mismatch err = %v", err)
	}
	model = newReplayModel("x", recording, nil, "prompt", "fixture")
	if err := model.Finish(); err == nil || !strings.Contains(err.Error(), "只用了") {
		t.Fatalf("unused err = %v", err)
	}
	model = newReplayModel("x", recording, nil, "prompt", "fixture")
	if _, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleSystem, Content: "sys"}, {Role: ai.RoleUser, Content: "u"}}, []ops.Tool{{Name: "get_me"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleSystem, Content: "sys"}, {Role: ai.RoleUser, Content: "u"}}, []ops.Tool{{Name: "get_me"}}); err == nil || !strings.Contains(err.Error(), "录制只有") {
		t.Fatalf("extra err = %v", err)
	}
	errorRecording := CaseRecording{PromptHash: "prompt", Fingerprint: "fixture", Calls: []RecordedCall{{Messages: 2, Tools: []string{}, System: "sys", Users: []string{"u"}, Error: "boom"}}}
	model = newReplayModel("x", errorRecording, nil, "prompt", "fixture")
	if _, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleSystem, Content: "sys"}, {Role: ai.RoleUser, Content: "u"}}, nil); err == nil || err.Error() != "boom" {
		t.Fatalf("recorded error = %v", err)
	}
}

func TestEvalFixturesAreIsolated(t *testing.T) {
	first := Seed(t)
	second := Seed(t)
	if first.Shop.Res.TenantID == second.Shop.Res.TenantID {
		t.Fatal("fixtures share a tenant")
	}
	params := map[string]any{
		"customer_id": first.Refs["customer:王姐"], "gender": "female", "designated": true,
		"items": []any{map[string]any{"item_id": first.Refs["item:洗剪吹"], "staff": []any{map[string]any{"slot": 1, "staff_id": first.Refs["staff:阿峰"]}}}},
	}
	first.Shop.Call(t, first.Shop.Owner, "create_draft", map[string]any{"operation": "open_order", "params": params, "summary": "隔离", "utterance": "隔离"}, 200)
	if got := len(first.Shop.Call(t, first.Shop.Owner, "list_drafts", map[string]any{}, 200).Body["drafts"].([]any)); got != 1 {
		t.Fatalf("first drafts = %d", got)
	}
	if got := len(second.Shop.Call(t, second.Shop.Owner, "list_drafts", map[string]any{}, 200).Body["drafts"].([]any)); got != 0 {
		t.Fatalf("second drafts = %d", got)
	}
}

func comparableCassette(cassette *Cassette) *Cassette {
	out := &Cassette{Version: cassette.Version, CaseFileHash: cassette.CaseFileHash, Cases: map[string]CaseRecording{}}
	for id, recording := range cassette.Cases {
		reverse := map[string]string{}
		refs := map[string]string{}
		for key, value := range recording.Refs {
			reverse[value] = key
			refs[key] = key
		}
		copied := CaseRecording{PromptHash: recording.PromptHash, Fingerprint: recording.Fingerprint, Refs: refs}
		for _, call := range recording.Calls {
			record := call
			if call.Response != nil {
				message := normalizeMessage(*call.Response, reverse)
				record.Response = &message
			}
			copied.Calls = append(copied.Calls, record)
		}
		out.Cases[id] = copied
	}
	return out
}

func normalizeMessage(message ai.Message, reverse map[string]string) ai.Message {
	out := message
	out.ToolCalls = make([]ai.ToolCall, len(message.ToolCalls))
	copy(out.ToolCalls, message.ToolCalls)
	for i, call := range out.ToolCalls {
		var args any
		if json.Unmarshal([]byte(call.Arguments), &args) != nil {
			continue
		}
		raw, err := json.Marshal(normalizeValue(args, reverse))
		if err == nil {
			out.ToolCalls[i].Arguments = string(raw)
		}
	}
	return out
}

func normalizeValue(value any, reverse map[string]string) any {
	switch v := value.(type) {
	case string:
		if key, ok := reverse[v]; ok {
			return key
		}
		if _, err := uuid.Parse(v); err == nil {
			return "<uuid>"
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = normalizeValue(item, reverse)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = normalizeValue(item, reverse)
		}
		return out
	default:
		return value
	}
}

func TestEvalScorerExtraCalls(t *testing.T) {
	base := Case{ID: "x", Story: "s", Category: "c", Role: "owner", Messages: []CaseMessage{{Role: "user", Content: "问"}}, AnswerContains: []string{"OK"}, ReferenceAnswer: "OK", Expected: []ExpectedCall{{Tool: "get_me"}}}
	readExtra := ScoreCase(base, Observed{Reply: "OK", Sources: []ai.Source{{Tool: "get_me", OK: true}, {Tool: "search_customers", OK: true, Arguments: map[string]any{"query": "王姐"}}}}, nil)
	if !readExtra.Overall {
		t.Fatalf("extra successful read call should pass: %+v", readExtra)
	}
	draftExtra := ScoreCase(base, Observed{
		Reply: "OK", Sources: []ai.Source{{Tool: "get_me", OK: true}, {Tool: "open_order", OK: true}},
		Drafts: []ai.DraftView{{Draft: drafts.Draft{ID: uuid.New(), Operation: "open_order"}}}, DraftTools: map[string]bool{"open_order": true},
	}, nil)
	if draftExtra.Overall || !strings.Contains(strings.Join(draftExtra.FailureReasons, "\n"), "多余的起草工具调用") {
		t.Fatalf("extra draft call should fail: %+v", draftExtra)
	}
	failedExtra := ScoreCase(base, Observed{Reply: "OK", Sources: []ai.Source{{Tool: "get_me", OK: true}, {Tool: "search_customers", OK: false, Error: "boom"}}}, nil)
	if failedExtra.Overall || !strings.Contains(strings.Join(failedExtra.FailureReasons, "\n"), "多余的工具调用失败") {
		t.Fatalf("extra failed call should fail: %+v", failedExtra)
	}
}

func TestEvalScorerAnswerNotContains(t *testing.T) {
	c := Case{ID: "x", Story: "s", Category: "draft", Role: "owner", Messages: []CaseMessage{{Role: "user", Content: "问"}}, AnswerContains: []string{"草稿"}, AnswerNotContains: []string{"已开单"}, ReferenceAnswer: "已生成草稿"}
	result := ScoreCase(c, Observed{Reply: "已开单，草稿"}, nil)
	if result.Overall || !strings.Contains(strings.Join(result.FailureReasons, "\n"), "回答出现禁止关键词") {
		t.Fatalf("banned answer should fail: %+v", result)
	}
}

func TestEvalScorerRefusal(t *testing.T) {
	c := Case{ID: "x", Story: "s", Category: "refusal", Refusal: true, Role: "manager", AllowedTools: []string{"get_me"}, Messages: []CaseMessage{{Role: "user", Content: "问"}}, AnswerContains: []string{"没有权限"}, ReferenceAnswer: "没有权限"}
	if result := ScoreCase(c, Observed{Reply: "没有权限", Sources: []ai.Source{{Tool: "get_me", OK: true}}}, nil); !result.Overall {
		t.Fatalf("allowed read should pass: %+v", result)
	}
	strayRead := ScoreCase(c, Observed{Reply: "没有权限", Sources: []ai.Source{{Tool: "list_performance", OK: true}}}, nil)
	if strayRead.Overall || !strings.Contains(strings.Join(strayRead.FailureReasons, "\n"), "未允许的工具") {
		t.Fatalf("stray offered read should fail: %+v", strayRead)
	}
	strayDraft := ScoreCase(c, Observed{
		Reply: "没有权限", Sources: []ai.Source{{Tool: "open_order", OK: true}},
		Drafts: []ai.DraftView{{Draft: drafts.Draft{ID: uuid.New(), Operation: "open_order"}}}, DraftTools: map[string]bool{"open_order": true},
	}, nil)
	if strayDraft.Overall || !strings.Contains(strings.Join(strayDraft.FailureReasons, "\n"), "不应生成草稿") {
		t.Fatalf("stray offered draft should fail: %+v", strayDraft)
	}
}

func TestEvalResolveCaseDoesNotMutateSuite(t *testing.T) {
	suite := mustSuite(t)
	var original Case
	for _, c := range suite.Cases {
		if c.ID == "draft-open-order" {
			original = c
		}
	}
	if original.ID == "" {
		t.Fatal("case not found")
	}
	first := Seed(t)
	second := Seed(t)
	firstResolved, err := ResolveCase(original, first)
	if err != nil {
		t.Fatal(err)
	}
	secondResolved, err := ResolveCase(original, second)
	if err != nil {
		t.Fatal(err)
	}
	if got := original.Expected[0].Args["customer_id"]; got != "customer:王姐" {
		t.Fatalf("source case mutated: %v", got)
	}
	firstID := firstResolved.Expected[0].Args["customer_id"]
	secondID := secondResolved.Expected[0].Args["customer_id"]
	if firstID != first.Refs["customer:王姐"] || secondID != second.Refs["customer:王姐"] || firstID == secondID {
		t.Fatalf("resolved ids = %v, %v (fixtures %v, %v)", firstID, secondID, first.Refs["customer:王姐"], second.Refs["customer:王姐"])
	}
}

func TestEvalReplayStaleness(t *testing.T) {
	recording := CaseRecording{PromptHash: "prompt", Fingerprint: "fixture", Calls: []RecordedCall{{
		Messages: 2, Tools: []string{}, System: "sys", Users: []string{"u"}, Response: &ai.Message{Role: ai.RoleAssistant, Content: "ok"},
	}}}
	matching := []ai.Message{{Role: ai.RoleSystem, Content: "sys"}, {Role: ai.RoleUser, Content: "u"}}
	for _, tc := range []struct {
		name        string
		prompt      string
		fingerprint string
		want        string
	}{
		{"prompt", "other", "fixture", "提示词指纹"},
		{"fixture", "prompt", "other", "fixture 指纹"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newReplayModel("x", recording, nil, tc.prompt, tc.fingerprint)
			if _, err := model.Chat(context.Background(), matching, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	model := newReplayModel("x", recording, nil, "prompt", "fixture")
	if _, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleSystem, Content: "other"}, {Role: ai.RoleUser, Content: "u"}}, nil); err == nil || !strings.Contains(err.Error(), "system 消息不一致") {
		t.Fatalf("system err = %v", err)
	}
	model = newReplayModel("x", recording, nil, "prompt", "fixture")
	if _, err := model.Chat(context.Background(), []ai.Message{{Role: ai.RoleSystem, Content: "sys"}, {Role: ai.RoleUser, Content: "other"}}, nil); err == nil || !strings.Contains(err.Error(), "user 消息不一致") {
		t.Fatalf("user err = %v", err)
	}
	malformed := CaseRecording{PromptHash: "prompt", Fingerprint: "fixture", Calls: []RecordedCall{{
		Messages: 2, Tools: []string{}, System: "sys", Users: []string{"u"},
		Response: &ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: "1", Name: "get_me", Arguments: "{"}}},
	}}}
	model = newReplayModel("x", malformed, nil, "prompt", "fixture")
	if _, err := model.Chat(context.Background(), matching, nil); err == nil || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("malformed cassette err = %v", err)
	}
}

func TestEvalAnyOfArgs(t *testing.T) {
	c := Case{ID: "x", Story: "s", Category: "read", Role: "owner", Messages: []CaseMessage{{Role: "user", Content: "问"}}, AnswerContains: []string{"OK"}, ReferenceAnswer: "OK", Expected: []ExpectedCall{{
		AnyOf: []string{"list_performance", "get_staff_report"},
		AnyOfArgs: []map[string]any{
			{"date_from": "2026-11-10", "date_to": "2026-11-10"},
			{"from": "2026-11-10", "to": "2026-11-10"},
		},
	}}}
	if result := ScoreCase(c, Observed{Reply: "OK", Sources: []ai.Source{{Tool: "get_staff_report", OK: true, Arguments: map[string]any{"from": "2026-11-10", "to": "2026-11-10"}}}}, nil); !result.Overall {
		t.Fatalf("second alternative args should pass: %+v", result)
	}
	if result := ScoreCase(c, Observed{Reply: "OK", Sources: []ai.Source{{Tool: "get_staff_report", OK: true, Arguments: map[string]any{"from": "2026-11-09", "to": "2026-11-10"}}}}, nil); result.Overall {
		t.Fatal("wrong alternative args should fail")
	}
}

func TestEvalSameStringAndNumber(t *testing.T) {
	for _, tc := range []struct {
		want, got string
		ok        bool
	}{
		{"88", "88.00", true},
		{"2026-11-10T15:00:00+08:00", "2026-11-10T07:00:00Z", true},
		{"550E8400-E29B-41D4-A716-446655440000", "550e8400-e29b-41d4-a716-446655440000", true},
		{"88", "89", false},
	} {
		if got := sameString(tc.want, tc.got); got != tc.ok {
			t.Fatalf("sameString(%q, %q) = %v, want %v", tc.want, tc.got, got, tc.ok)
		}
	}
	for _, tc := range []struct {
		want float64
		got  any
		ok   bool
	}{
		{88, json.Number("88.00"), true},
		{88, "88", true},
		{88, "88.01", false},
	} {
		if got := sameNumber(tc.want, tc.got); got != tc.ok {
			t.Fatalf("sameNumber(%v, %v) = %v, want %v", tc.want, tc.got, got, tc.ok)
		}
	}
}

func TestEvalRefusalOfferedTools(t *testing.T) {
	suite := mustSuite(t)
	for _, c := range suite.Cases {
		if c.Category != "refusal" {
			continue
		}
		f := Seed(t)
		caller := f.Callers[c.Role]
		tools := f.Shop.Registry.ChatTools(caller)
		offered := map[string]bool{}
		for _, tool := range tools {
			offered[tool.Name] = true
		}
		for _, name := range c.AllowedTools {
			if !offered[name] {
				t.Fatalf("%s: allowed tool %s is not offered to %s", c.ID, name, c.Role)
			}
			if f.Shop.Registry.DraftTool(name) || name == "update_draft_params" {
				t.Fatalf("%s: allowed tool %s creates a draft", c.ID, name)
			}
		}
		offeredForbidden := 0
		for _, name := range c.Forbidden {
			if offered[name] {
				offeredForbidden++
			}
		}
		if offeredForbidden == 0 {
			t.Fatalf("%s: no forbidden tool is actually offered to %s", c.ID, c.Role)
		}
		t.Logf("%s role=%s offered=%v", c.ID, c.Role, toolNames(tools))
	}
}

func TestEvalFixtureFingerprintStable(t *testing.T) {
	first := Seed(t)
	second := Seed(t)
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("same fixture source produced different fingerprints: %s vs %s", first.Fingerprint, second.Fingerprint)
	}
}

func TestEvalFixtureFingerprintChangesWithSource(t *testing.T) {
	source := []byte("package eval\n// fixture seed\n")
	changed := []byte("package eval\n// fixture seed changed\n")
	refs := []string{"customer:王姐", "item:洗剪吹"}
	if fingerprintFixtureSource(source, refs) == fingerprintFixtureSource(changed, refs) {
		t.Fatal("changed fixture source must change the fingerprint")
	}
	if fingerprintFixtureSource(source, refs) == fingerprintFixtureSource(source, []string{"customer:王姐"}) {
		t.Fatal("changed symbolic refs must change the fingerprint")
	}
	if fingerprintFixtureSource(source, refs) != fingerprintFixtureSource(source, []string{"customer:王姐", "item:洗剪吹"}) {
		t.Fatal("fingerprint must be deterministic")
	}
}
