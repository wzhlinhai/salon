package eval

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/db"
	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
	"github.com/wzhlinhai/salon/backend/internal/settings"
)

// ModelFactory 为一条用例返回模型；oracle、回放和真实模型都通过它接入。
type ModelFactory func(c Case, f *Fixture) ai.Model

// RunConfig 是报告元数据，BaseURLHost 只填主机名。
type RunConfig struct {
	ModelName   string
	BaseURLHost string
	GitCommit   string
	Timestamp   time.Time
	// Now 是报告时间和耗时用的时钟；不填时使用固定评测时刻，耗时记为零。
	Now func() time.Time
}

// Run 为每条用例新建隔离门店并执行一次对话，返回完整报告。
func Run(ctx context.Context, t testing.TB, suite *Suite, cfg RunConfig, factory ModelFactory) (Report, error) {
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return fixtureNow }
	}
	if cfg.Timestamp.IsZero() {
		cfg.Timestamp = cfg.Now().UTC()
	}
	report := Report{Run: RunMeta{
		Model: cfg.ModelName, BaseURLHost: cfg.BaseURLHost, GitCommit: cfg.GitCommit,
		Timestamp: cfg.Timestamp, CaseFileHash: suite.Hash, CaseCount: len(suite.Cases),
	}}
	promptTools := map[string]bool{}
	for _, c := range suite.Cases {
		child := &caseTB{TB: t}
		fixture := Seed(child)
		caller, ok := fixture.Callers[c.Role]
		if !ok {
			child.runCleanups()
			return Report{}, fmt.Errorf("%s: 没有角色 %q 的调用者", c.ID, c.Role)
		}
		resolved, err := ResolveCase(c, fixture)
		if err != nil {
			child.runCleanups()
			return Report{}, err
		}
		model := factory(resolved, fixture)
		counted := &countingModel{inner: model}
		chat := ai.NewChat(fixture.Shop.Registry, func() ai.Model { return counted }, fixture.Shop.Now, businessDate(fixture.Shop.Pool))
		started := cfg.Now()
		answer, askErr := chat.Ask(ctx, caller, aiMessages(resolved.Messages))
		latency := cfg.Now().Sub(started)
		if askErr == nil {
			if finisher, ok := model.(interface{ Finish() error }); ok {
				askErr = finisher.Finish()
			}
		}
		draftTools := map[string]bool{}
		for _, source := range answer.Sources {
			draftTools[source.Tool] = fixture.Shop.Registry.DraftTool(source.Tool)
		}
		result := ScoreCase(resolved, Observed{Reply: answer.Reply, Sources: answer.Sources, Drafts: answer.Drafts, DraftTools: draftTools}, askErr)
		result.LatencyMS = latency.Milliseconds()
		result.ModelCalls = counted.Count()
		tools := fixture.Shop.Registry.ChatTools(caller)
		if resolved.Refusal || resolved.Category == "refusal" {
			result.OfferedTools = toolNames(tools)
		}
		result.PromptHash = ai.PromptFingerprint(tools)
		for _, tool := range tools {
			promptTools[tool.Name] = true
		}
		report.Cases = append(report.Cases, result)
		child.runCleanups()
	}
	names := make([]string, 0, len(promptTools))
	for name := range promptTools {
		names = append(names, name)
	}
	sort.Strings(names)
	tools := make([]ops.Tool, len(names))
	for i, name := range names {
		tools[i] = ops.Tool{Name: name}
	}
	report.Run.PromptHash = ai.PromptFingerprint(tools)
	report.Aggregates = Aggregate(report.Cases, false)
	report.KnownGaps = Aggregate(report.Cases, true)
	return report, nil
}

// GitCommit 返回当前 git 提交；拿不到时为空。
func GitCommit() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type countingModel struct {
	inner ai.Model
	calls int
}

func (m *countingModel) Chat(ctx context.Context, messages []ai.Message, tools []ops.Tool) (ai.Message, error) {
	m.calls++
	return m.inner.Chat(ctx, messages, tools)
}

func (m *countingModel) Count() int { return m.calls }

func aiMessages(messages []CaseMessage) []ai.Message {
	out := make([]ai.Message, len(messages))
	for i, message := range messages {
		out[i] = ai.Message{Role: message.Role, Content: message.Content}
	}
	return out
}

func businessDate(pool *pgxpool.Pool) ai.BusinessDateFunc {
	return func(ctx context.Context, caller ops.Caller, now time.Time) (time.Time, error) {
		var day time.Time
		err := db.InTenant(ctx, pool, caller.TenantID, func(q *dbgen.Queries) error {
			var err error
			day, err = settings.BusinessDate(ctx, q, caller.StoreID, now)
			return err
		})
		return day, err
	}
}

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

// CLITB 是命令行使用的 testing.TB 替身，只实现 dbtest 和 optest 用到的方法。
type CLITB struct {
	testing.TB
	mu       sync.Mutex
	cleanups []func()
	errors   []string
}

// NewCLITB 返回一个命令行测试替身。
func NewCLITB() *CLITB { return &CLITB{} }

func (c *CLITB) Helper() {}

func (c *CLITB) Cleanup(f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanups = append(c.cleanups, f)
}

func (c *CLITB) Fatal(args ...any) { panic(fmt.Sprint(args...)) }

func (c *CLITB) Fatalf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }

func (c *CLITB) Error(args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, fmt.Sprint(args...))
}

func (c *CLITB) Errorf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, fmt.Sprintf(format, args...))
}

// RunCleanups 执行命令行替身收集到的清理函数。
func (c *CLITB) RunCleanups() {
	c.mu.Lock()
	cleanups := c.cleanups
	c.cleanups = nil
	c.mu.Unlock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
}

// Errors 返回命令行替身收到的非致命错误。
func (c *CLITB) Errors() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.errors...)
}
