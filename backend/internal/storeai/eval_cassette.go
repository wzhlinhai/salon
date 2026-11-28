package storeai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

// EvalCassette 是门店 AI 评测按用例和调用顺序保存的模型响应。
type EvalCassette struct {
	Version      int                          `json:"version"`
	CaseFileHash string                       `json:"case_file_hash"`
	Cases        map[string]EvalCaseRecording `json:"cases"`
}

// EvalCaseRecording 是一条用例的全部模型响应和录制时的符号引用。
type EvalCaseRecording struct {
	Refs  map[string]string  `json:"refs,omitempty"`
	Calls []EvalRecordedCall `json:"calls"`
}

// EvalRecordedCall 是一次模型调用的回放校验信息和响应。
type EvalRecordedCall struct {
	Messages int         `json:"messages"`
	Tools    []string    `json:"tools,omitempty"`
	System   string      `json:"system,omitempty"`
	Users    []string    `json:"users,omitempty"`
	Response *ai.Message `json:"response,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// EvalRecorder 记录每个用例的模型调用。
type EvalRecorder struct {
	mu    sync.Mutex
	cases map[string]*EvalCaseRecording
}

// NewEvalRecorder 返回一个空的记录器。
func NewEvalRecorder() *EvalRecorder {
	return &EvalRecorder{cases: map[string]*EvalCaseRecording{}}
}

// Model 把 inner 包成记录模型。
func (r *EvalRecorder) Model(caseID string, refs map[string]string, inner ai.Model) ai.Model {
	return &evalRecordingModel{recorder: r, caseID: caseID, refs: cloneEvalRefs(refs), inner: inner}
}

// Cassette 返回记录好的 cassette。
func (r *EvalRecorder) Cassette(caseFileHash string) *EvalCassette {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := &EvalCassette{Version: 1, CaseFileHash: caseFileHash, Cases: map[string]EvalCaseRecording{}}
	for id, recording := range r.cases {
		out.Cases[id] = *recording
	}
	return out
}

type evalRecordingModel struct {
	recorder *EvalRecorder
	caseID   string
	refs     map[string]string
	inner    ai.Model
}

func (m *evalRecordingModel) Chat(ctx context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	message, err := m.inner.Chat(ctx, messages, tools)
	system, users := evalMessageContents(messages)
	record := EvalRecordedCall{Messages: len(messages), Tools: evalToolNames(tools), System: system, Users: users}
	if err != nil {
		record.Error = err.Error()
	} else {
		copied := message
		record.Response = &copied
	}
	m.recorder.mu.Lock()
	recording := m.recorder.cases[m.caseID]
	if recording == nil {
		recording = &EvalCaseRecording{Refs: cloneEvalRefs(m.refs)}
		m.recorder.cases[m.caseID] = recording
	}
	recording.Calls = append(recording.Calls, record)
	m.recorder.mu.Unlock()
	return message, err
}

// EvalReplay 按用例 id 和调用顺序重放 cassette。
type EvalReplay struct {
	cassette *EvalCassette
}

// NewEvalReplay 返回一份 cassette 的重放器。
func NewEvalReplay(cassette *EvalCassette) *EvalReplay {
	return &EvalReplay{cassette: cassette}
}

// Model 返回一条用例的重放模型；currentRefs 用于把录制时的 ID 换成本次 fixture 的 ID。
func (r *EvalReplay) Model(caseID string, currentRefs map[string]string) ai.Model {
	recording := r.cassette.Cases[caseID]
	rewrite := map[string]string{}
	for key, oldID := range recording.Refs {
		if newID, ok := currentRefs[key]; ok && oldID != newID {
			rewrite[oldID] = newID
		}
	}
	return &evalReplayModel{caseID: caseID, recording: recording, rewrite: rewrite}
}

type evalReplayModel struct {
	caseID    string
	recording EvalCaseRecording
	rewrite   map[string]string
	call      int
	err       error
}

func (m *evalReplayModel) Chat(_ context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	if m.call >= len(m.recording.Calls) {
		m.err = fmt.Errorf("replay cassette %s: 模型调用了 %d 次，录制只有 %d 次", m.caseID, m.call+1, len(m.recording.Calls))
		return ai.Message{}, m.err
	}
	record := m.recording.Calls[m.call]
	m.call++
	if record.Messages != len(messages) {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用：消息数 %d，录制为 %d", m.caseID, m.call, len(messages), record.Messages)
		return ai.Message{}, m.err
	}
	if names := evalToolNames(tools); !equalEvalStrings(names, record.Tools) {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用：工具列表 %v，录制为 %v", m.caseID, m.call, names, record.Tools)
		return ai.Message{}, m.err
	}
	system, users := evalMessageContents(messages)
	if system != evalRewriteText(record.System, m.rewrite) {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用：system 消息不一致", m.caseID, m.call)
		return ai.Message{}, m.err
	}
	recordedUsers := make([]string, len(record.Users))
	for i, user := range record.Users {
		recordedUsers[i] = evalRewriteText(user, m.rewrite)
	}
	if !equalEvalStrings(users, recordedUsers) {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用：user 消息不一致", m.caseID, m.call)
		return ai.Message{}, m.err
	}
	if record.Error != "" {
		m.err = errors.New(record.Error)
		return ai.Message{}, m.err
	}
	if record.Response == nil {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用没有响应", m.caseID, m.call)
		return ai.Message{}, m.err
	}
	message, err := evalRewriteMessage(*record.Response, m.rewrite)
	if err != nil {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用：%w", m.caseID, m.call, err)
		return ai.Message{}, m.err
	}
	return message, nil
}

func (m *evalReplayModel) Finish() error {
	if m.err != nil {
		return m.err
	}
	if m.call != len(m.recording.Calls) {
		return fmt.Errorf("replay cassette %s: 录制有 %d 次调用，实际只用了 %d 次", m.caseID, len(m.recording.Calls), m.call)
	}
	return nil
}

// LoadEvalCassette 读取一份 cassette JSON 文件。
func LoadEvalCassette(path string) (*EvalCassette, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cassette EvalCassette
	if err := json.Unmarshal(raw, &cassette); err != nil {
		return nil, fmt.Errorf("parse cassette %s: %w", path, err)
	}
	if cassette.Version != 1 {
		return nil, fmt.Errorf("不支持 cassette 版本 %d", cassette.Version)
	}
	return &cassette, nil
}

// SaveEvalCassette 写出一份 cassette JSON 文件。
func SaveEvalCassette(path string, cassette *EvalCassette) error {
	raw, err := json.MarshalIndent(cassette, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func evalToolNames(tools []ops.Tool) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	sort.Strings(out)
	return out
}

func equalEvalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneEvalRefs(refs map[string]string) map[string]string {
	out := make(map[string]string, len(refs))
	for key, value := range refs {
		out[key] = value
	}
	return out
}

func evalMessageContents(messages []ai.Message) (string, []string) {
	var system string
	users := []string{}
	for _, message := range messages {
		switch message.Role {
		case ai.RoleSystem:
			if system == "" {
				system = message.Content
			}
		case ai.RoleUser:
			users = append(users, message.Content)
		}
	}
	return system, users
}

func evalRewriteText(text string, rewrite map[string]string) string {
	if len(rewrite) == 0 {
		return text
	}
	pairs := make([]string, 0, len(rewrite)*2)
	for oldID, newID := range rewrite {
		pairs = append(pairs, oldID, newID)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

func evalRewriteMessage(message ai.Message, rewrite map[string]string) (ai.Message, error) {
	if len(message.ToolCalls) == 0 {
		return message, nil
	}
	out := message
	out.ToolCalls = make([]ai.ToolCall, len(message.ToolCalls))
	copy(out.ToolCalls, message.ToolCalls)
	for i, call := range out.ToolCalls {
		var args any
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return ai.Message{}, fmt.Errorf("工具 %s 参数不是合法 JSON：%w", call.Name, err)
		}
		raw, err := json.Marshal(evalRewriteValue(args, rewrite))
		if err != nil {
			return ai.Message{}, fmt.Errorf("工具 %s 参数无法重新编码：%w", call.Name, err)
		}
		out.ToolCalls[i].Arguments = string(raw)
	}
	return out, nil
}

func evalRewriteValue(value any, rewrite map[string]string) any {
	switch v := value.(type) {
	case string:
		if replaced, ok := rewrite[v]; ok {
			return replaced
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = evalRewriteValue(item, rewrite)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = evalRewriteValue(item, rewrite)
		}
		return out
	default:
		return value
	}
}
