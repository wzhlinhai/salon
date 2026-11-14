package eval

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

// Cassette 是按用例和调用顺序保存的模型响应，用于离线重放。
type Cassette struct {
	Version      int                      `json:"version"`
	CaseFileHash string                   `json:"case_file_hash"`
	Cases        map[string]CaseRecording `json:"cases"`
}

// CaseRecording 是一条用例的全部模型响应、当时的符号引用和指纹。
type CaseRecording struct {
	PromptHash  string            `json:"prompt_hash,omitempty"`
	Fingerprint string            `json:"fixture_fingerprint,omitempty"`
	Refs        map[string]string `json:"refs,omitempty"`
	Calls       []RecordedCall    `json:"calls"`
}

// RecordedCall 是一次模型响应的回放校验信息和内容。
type RecordedCall struct {
	Messages int         `json:"messages"`
	Tools    []string    `json:"tools"`
	System   string      `json:"system,omitempty"`
	Users    []string    `json:"users,omitempty"`
	Response *ai.Message `json:"response,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// Recorder 把每个用例的模型响应记录下来。
type Recorder struct {
	mu    sync.Mutex
	cases map[string]*CaseRecording
}

// NewRecorder 返回一个空的记录器。
func NewRecorder() *Recorder { return &Recorder{cases: map[string]*CaseRecording{}} }

// Model 返回包在 inner 外面的记录模型；用例和 fixture 提供提示词、fixture 指纹和符号引用。
func (r *Recorder) Model(c Case, f *Fixture, inner ai.Model) ai.Model {
	promptHash := ai.PromptFingerprint(f.Shop.Registry.ChatTools(f.Callers[c.Role]))
	return &recordingModel{recorder: r, caseID: c.ID, inner: inner, refs: cloneRefs(f.Refs), promptHash: promptHash, fingerprint: f.Fingerprint}
}

// Cassette 返回记录好的回放文件。
func (r *Recorder) Cassette(caseFileHash string) *Cassette {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := &Cassette{Version: 1, CaseFileHash: caseFileHash, Cases: map[string]CaseRecording{}}
	for id, recording := range r.cases {
		out.Cases[id] = *recording
	}
	return out
}

type recordingModel struct {
	recorder    *Recorder
	caseID      string
	inner       ai.Model
	refs        map[string]string
	promptHash  string
	fingerprint string
}

func (m *recordingModel) Chat(ctx context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	message, err := m.inner.Chat(ctx, messages, tools)
	system, users := messageContents(messages)
	record := RecordedCall{Messages: len(messages), Tools: toolNames(tools), System: system, Users: users}
	if err != nil {
		record.Error = err.Error()
	} else {
		copied := message
		record.Response = &copied
	}
	m.recorder.mu.Lock()
	recording := m.recorder.cases[m.caseID]
	if recording == nil {
		recording = &CaseRecording{PromptHash: m.promptHash, Fingerprint: m.fingerprint, Refs: cloneRefs(m.refs)}
		m.recorder.cases[m.caseID] = recording
	}
	recording.Calls = append(recording.Calls, record)
	m.recorder.mu.Unlock()
	return message, err
}

// Replay 按用例 id 和调用顺序重放一份 cassette。
type Replay struct {
	cassette *Cassette
}

// NewReplay 返回一份 cassette 的重放器。
func NewReplay(cassette *Cassette) *Replay { return &Replay{cassette: cassette} }

// Model 返回一条用例的重放模型；用例和 fixture 用于校验提示词、fixture 指纹并映射 ID。
func (r *Replay) Model(c Case, f *Fixture) ai.Model {
	recording := r.cassette.Cases[c.ID]
	promptHash := ai.PromptFingerprint(f.Shop.Registry.ChatTools(f.Callers[c.Role]))
	return newReplayModel(c.ID, recording, f.Refs, promptHash, f.Fingerprint)
}

func newReplayModel(caseID string, recording CaseRecording, currentRefs map[string]string, promptHash, fingerprint string) *replayModel {
	rewrite := map[string]string{}
	for key, oldID := range recording.Refs {
		if newID, ok := currentRefs[key]; ok && oldID != newID {
			rewrite[oldID] = newID
		}
	}
	model := &replayModel{caseID: caseID, recording: recording, rewrite: rewrite, promptHash: promptHash, fingerprint: fingerprint}
	model.configErr = replayConfigError(caseID, recording, promptHash, fingerprint)
	return model
}

func replayConfigError(caseID string, recording CaseRecording, promptHash, fingerprint string) error {
	if recording.PromptHash != promptHash {
		return fmt.Errorf("replay cassette %s: 提示词指纹不一致（录制 %s，当前 %s）", caseID, shortFingerprint(recording.PromptHash), shortFingerprint(promptHash))
	}
	if recording.Fingerprint != fingerprint {
		return fmt.Errorf("replay cassette %s: fixture 指纹不一致（录制 %s，当前 %s）", caseID, shortFingerprint(recording.Fingerprint), shortFingerprint(fingerprint))
	}
	return nil
}

type replayModel struct {
	caseID      string
	recording   CaseRecording
	rewrite     map[string]string
	promptHash  string
	fingerprint string
	configErr   error
	call        int
	err         error
}

func (m *replayModel) Chat(_ context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	if m.configErr != nil {
		m.err = m.configErr
		return ai.Message{}, m.err
	}
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
	names := toolNames(tools)
	if !equalStrings(names, record.Tools) {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用：工具列表 %v，录制为 %v", m.caseID, m.call, names, record.Tools)
		return ai.Message{}, m.err
	}
	system, users := messageContents(messages)
	if system != rewriteText(record.System, m.rewrite) {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用：system 消息不一致", m.caseID, m.call)
		return ai.Message{}, m.err
	}
	recordedUsers := make([]string, len(record.Users))
	for i, user := range record.Users {
		recordedUsers[i] = rewriteText(user, m.rewrite)
	}
	if !equalStrings(users, recordedUsers) {
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
	message, err := rewriteMessage(*record.Response, m.rewrite, messages)
	if err != nil {
		m.err = fmt.Errorf("replay cassette %s 第 %d 次调用：%w", m.caseID, m.call, err)
		return ai.Message{}, m.err
	}
	return message, nil
}

func (m *replayModel) Finish() error {
	if m.err != nil {
		return m.err
	}
	if m.call != len(m.recording.Calls) {
		return fmt.Errorf("replay cassette %s: 录制有 %d 次调用，实际只用了 %d 次", m.caseID, len(m.recording.Calls), m.call)
	}
	return nil
}

// LoadCassette 读取一份 cassette JSON 文件。
func LoadCassette(path string) (*Cassette, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cassette Cassette
	if err := json.Unmarshal(raw, &cassette); err != nil {
		return nil, fmt.Errorf("parse cassette %s: %w", path, err)
	}
	if cassette.Version != 1 {
		return nil, fmt.Errorf("不支持 cassette 版本 %d", cassette.Version)
	}
	return &cassette, nil
}

// SaveCassette 写出一份 cassette JSON 文件。
func SaveCassette(path string, cassette *Cassette) error {
	raw, err := json.MarshalIndent(cassette, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func toolNames(tools []ops.Tool) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = tool.Name
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
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

func cloneRefs(refs map[string]string) map[string]string {
	out := make(map[string]string, len(refs))
	for key, value := range refs {
		out[key] = value
	}
	return out
}

func messageContents(messages []ai.Message) (string, []string) {
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

func rewriteText(text string, rewrite map[string]string) string {
	if len(rewrite) == 0 {
		return text
	}
	pairs := make([]string, 0, len(rewrite)*2)
	for oldID, newID := range rewrite {
		pairs = append(pairs, oldID, newID)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

func rewriteMessage(message ai.Message, rewrite map[string]string, messages []ai.Message) (ai.Message, error) {
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
		args = rewriteValue(args, rewrite)
		if call.Name == "update_draft_params" {
			if draft := lastDraft(messages); draft != nil {
				if object, ok := args.(map[string]any); ok {
					object["id"] = draft["id"]
				}
			}
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return ai.Message{}, fmt.Errorf("工具 %s 参数无法重新编码：%w", call.Name, err)
		}
		out.ToolCalls[i].Arguments = string(raw)
	}
	return out, nil
}

func rewriteValue(value any, rewrite map[string]string) any {
	switch v := value.(type) {
	case string:
		if replaced, ok := rewrite[v]; ok {
			return replaced
		}
		return v
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = rewriteValue(item, rewrite)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = rewriteValue(item, rewrite)
		}
		return out
	default:
		return value
	}
}

func shortFingerprint(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
