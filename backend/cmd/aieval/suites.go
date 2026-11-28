package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ai/eval"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
	storeaieval "github.com/wzhlinhai/salon/backend/internal/storeai/eval"
)

type suiteOptions struct {
	out    string
	record string
	replay string
}

func parseSuiteOptions(name string, args []string) (suiteOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var opts suiteOptions
	fs.StringVar(&opts.out, "out", "", "写入 JSON 报告")
	fs.StringVar(&opts.record, "record", "", "把模型响应记录为 cassette")
	fs.StringVar(&opts.replay, "replay", "", "离线重放 cassette，不访问模型")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, errors.New(usage)
	}
	if opts.record != "" && opts.replay != "" {
		return opts, errors.New("--record 和 --replay 不能同时使用")
	}
	return opts, nil
}

func runClassifierSuite(args []string) error {
	opts, err := parseSuiteOptions("classifier", args)
	if err != nil {
		return err
	}
	suite, err := storeai.LoadClassifierSuite()
	if err != nil {
		return err
	}
	modelName := envOr("AI_MODEL", ai.DefaultModel)
	baseURL := envOr("AI_BASE_URL", ai.DefaultBaseURL)
	host := hostOnly(baseURL)
	var factory storeaieval.ClassifierModelFactory
	var recorder *storeai.EvalRecorder
	switch {
	case opts.replay != "":
		cassette, err := storeai.LoadEvalCassette(opts.replay)
		if err != nil {
			return err
		}
		if cassette.CaseFileHash != suite.Hash {
			return fmt.Errorf("cassette 的用例文件哈希 %s 与当前用例 %s 不同，请重新录制", cassette.CaseFileHash, suite.Hash)
		}
		replay := storeai.NewEvalReplay(cassette)
		modelName, host = "replay", "offline"
		factory = func(c storeai.ClassifierCase, refs map[string]string) ai.Model {
			return replay.Model(c.ID, refs)
		}
	default:
		model, err := openEvalModel()
		if err != nil {
			return err
		}
		factory = func(c storeai.ClassifierCase, _ map[string]string) ai.Model { return model }
		if opts.record != "" {
			recorder = storeai.NewEvalRecorder()
			factory = func(c storeai.ClassifierCase, refs map[string]string) ai.Model {
				return recorder.Model(c.ID, refs, model)
			}
		}
	}
	tb := eval.NewCLITB()
	report, err := storeaieval.RunClassifierSuite(context.Background(), tb, suite, modelName, host, factory)
	if err != nil {
		tb.RunCleanups()
		return err
	}
	if err := checkCleanup(tb); err != nil {
		return err
	}
	if opts.out != "" {
		if err := writeJSON(opts.out, report); err != nil {
			return err
		}
	}
	if recorder != nil && opts.record != "" {
		if err := storeai.SaveEvalCassette(opts.record, recorder.Cassette(suite.Hash)); err != nil {
			return err
		}
	}
	printClassifierReport(report)
	return nil
}

func runAgentSuite(args []string) error {
	opts, err := parseSuiteOptions("agent", args)
	if err != nil {
		return err
	}
	suite, err := storeai.LoadAgentSuite()
	if err != nil {
		return err
	}
	modelName := envOr("AI_MODEL", ai.DefaultModel)
	baseURL := envOr("AI_BASE_URL", ai.DefaultBaseURL)
	host := hostOnly(baseURL)
	var factory storeaieval.AgentModelFactory
	var recorder *storeai.EvalRecorder
	switch {
	case opts.replay != "":
		cassette, err := storeai.LoadEvalCassette(opts.replay)
		if err != nil {
			return err
		}
		if cassette.CaseFileHash != suite.Hash {
			return fmt.Errorf("cassette 的用例文件哈希 %s 与当前用例 %s 不同，请重新录制", cassette.CaseFileHash, suite.Hash)
		}
		replay := storeai.NewEvalReplay(cassette)
		modelName, host = "replay", "offline"
		factory = func(c storeai.AgentCase, f *eval.Fixture) ai.Model {
			return replay.Model(c.ID, f.Refs)
		}
	default:
		model, err := openEvalModel()
		if err != nil {
			return err
		}
		factory = func(_ storeai.AgentCase, _ *eval.Fixture) ai.Model { return model }
		if opts.record != "" {
			recorder = storeai.NewEvalRecorder()
			factory = func(c storeai.AgentCase, f *eval.Fixture) ai.Model {
				return recorder.Model(c.ID, f.Refs, model)
			}
		}
	}
	tb := eval.NewCLITB()
	report, err := storeaieval.RunAgentSuite(context.Background(), tb, suite, modelName, host, factory)
	if err != nil {
		tb.RunCleanups()
		return err
	}
	if err := checkCleanup(tb); err != nil {
		return err
	}
	if opts.out != "" {
		if err := writeJSON(opts.out, report); err != nil {
			return err
		}
	}
	if recorder != nil && opts.record != "" {
		if err := storeai.SaveEvalCassette(opts.record, recorder.Cassette(suite.Hash)); err != nil {
			return err
		}
	}
	printAgentReport(report)
	return nil
}

func openEvalModel() (ai.Model, error) {
	cfg, err := ai.OpenAIConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if cfg.APIKey == "" {
		return nil, errors.New("AI_API_KEY 未设置；请配置后用 aieval classifier/agent，或使用 --replay 离线重放")
	}
	return ai.NewOpenAIModel(cfg), nil
}

func printClassifierReport(report storeaieval.ClassifierReport) {
	fmt.Printf("分类器评测：%s（%s）\n", report.Model, report.BaseURLHost)
	fmt.Printf("用例 %d 条，用例文件 %s\n", report.Total, shortHash(report.CaseFileHash))
	fmt.Printf("%-30s %-7s %-7s %-7s %-6s %s\n", "用例", "期望", "实际", "来源", "命中", "原因")
	for _, result := range report.Cases {
		source := result.ActualSource
		if result.ExpectedSource != result.ActualSource {
			source = result.ExpectedSource + "→" + result.ActualSource
		}
		reason := result.Reason
		if reason == "" {
			reason = result.Error
		}
		fmt.Printf("%-30s %-7s %-7s %-7s %-6s %s\n", result.ID,
			result.ExpectedDecision, result.ActualDecision, source, passMark(result.Hit), reason)
	}
	fmt.Printf("\n准确率 %.1f%%（%d/%d）\n", report.Accuracy*100, report.Pass, report.Total)
}

func printAgentReport(report storeaieval.AgentReport) {
	fmt.Printf("门店运行评测：%s（%s）\n", report.Model, report.BaseURLHost)
	fmt.Printf("用例 %d 条，用例文件 %s\n", report.Total, shortHash(report.CaseFileHash))
	fmt.Printf("%-34s %-6s %s\n", "用例", "命中", "期望 → 实际")
	for _, result := range report.Cases {
		expected := formatAgentCalls(result.Expected)
		actual := formatAgentCalls(result.Actual)
		if result.Error != "" {
			actual = result.Error
		}
		fmt.Printf("%-34s %-6s %s → %s\n", result.ID, passMark(result.Hit), expected, actual)
	}
	fmt.Printf("\n准确率 %.1f%%（%d/%d）\n", report.Accuracy*100, report.Pass, report.Total)
}

func formatAgentCalls(calls []storeaieval.AgentCall) string {
	if len(calls) == 0 {
		return "（无）"
	}
	parts := make([]string, len(calls))
	for i, call := range calls {
		raw, _ := json.Marshal(call.Args)
		parts[i] = call.Tool + string(raw)
	}
	return strings.Join(parts, " → ")
}
