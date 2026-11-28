package storeaieval

import (
	"encoding/json"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ai"
)

func agentHistory(t *testing.T, messages []ai.Message) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func agentToolCall(id, name string, args map[string]any) ai.Message {
	raw, _ := json.Marshal(args)
	return ai.Message{Role: ai.RoleAssistant, ToolCalls: []ai.ToolCall{{ID: id, Name: name, Arguments: string(raw)}}}
}

func agentToolResult(id, content string) ai.Message {
	return ai.Message{Role: ai.RoleTool, ToolCallID: id, Content: content}
}

func TestObservedAgentCallsIgnoreReadOnly(t *testing.T) {
	raw := agentHistory(t, []ai.Message{
		agentToolCall("read-1", "get_redemption", map[string]any{"key": "x"}),
		agentToolResult("read-1", `{"id":"x"}`),
		agentToolCall("write-1", "schedule_follow_up", map[string]any{"subject_kind": "coupon_redemption", "subject_id": "x", "minutes": 30}),
		agentToolResult("write-1", `{"timer_id":"t"}`),
	})
	actual, err := observedCalls(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 1 || actual[0].Tool != "schedule_follow_up" || actual[0].Error != "" {
		t.Fatalf("actual = %+v", actual)
	}
	expected := []AgentCall{{Tool: "schedule_follow_up", Args: map[string]any{"minutes": float64(30)}}}
	if !matchAgentCalls(expected, actual) {
		t.Fatalf("read-only interleaving should not fail: expected=%+v actual=%+v", expected, actual)
	}
}

func TestMatchAgentCallsFailsOnToolError(t *testing.T) {
	raw := agentHistory(t, []ai.Message{
		agentToolCall("write-1", "schedule_follow_up", map[string]any{"subject_kind": "coupon_redemption", "subject_id": "x", "minutes": 30}),
		agentToolResult("write-1", "对象不在本店"),
	})
	actual, err := observedCalls(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 1 || actual[0].Error == "" {
		t.Fatalf("actual = %+v", actual)
	}
	expected := []AgentCall{{Tool: "schedule_follow_up", Args: map[string]any{"minutes": float64(30)}}}
	if matchAgentCalls(expected, actual) {
		t.Fatal("tool error must fail the case")
	}
}

func TestMatchAgentCallsRejectsUnexpectedWrite(t *testing.T) {
	raw := agentHistory(t, []ai.Message{
		agentToolCall("read-1", "list_redemptions", map[string]any{"unlinked_only": true}),
		agentToolResult("read-1", `{"items":[]}`),
		agentToolCall("write-1", "schedule_follow_up", map[string]any{"subject_kind": "coupon_redemption", "subject_id": "x", "minutes": 30}),
		agentToolResult("write-1", `{"timer_id":"t"}`),
		agentToolCall("write-2", "create_request", map[string]any{"subject_kind": "coupon_redemption", "subject_id": "x"}),
		agentToolResult("write-2", `{"request_id":"r"}`),
	})
	actual, err := observedCalls(raw)
	if err != nil {
		t.Fatal(err)
	}
	expected := []AgentCall{{Tool: "schedule_follow_up", Args: map[string]any{"minutes": float64(30)}}}
	if matchAgentCalls(expected, actual) {
		t.Fatalf("unexpected write must fail: actual=%+v", actual)
	}
}

func TestMatchAgentCallsAllowsTrailingFinish(t *testing.T) {
	raw := agentHistory(t, []ai.Message{
		agentToolCall("write-1", "schedule_follow_up", map[string]any{"subject_kind": "coupon_redemption", "subject_id": "x", "minutes": 30}),
		agentToolResult("write-1", `{"timer_id":"t"}`),
		agentToolCall("write-2", "finish", map[string]any{"status": "waiting"}),
		agentToolResult("write-2", `{"status":"waiting"}`),
	})
	actual, err := observedCalls(raw)
	if err != nil {
		t.Fatal(err)
	}
	expected := []AgentCall{{Tool: "schedule_follow_up", Args: map[string]any{"minutes": float64(30)}}}
	if !matchAgentCalls(expected, actual) {
		t.Fatalf("trailing finish should be allowed: actual=%+v", actual)
	}
}
