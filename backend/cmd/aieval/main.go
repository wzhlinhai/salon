// aieval 运行 AI 对话评测集，支持真实模型、离线重放和两份报告对比。
//
//	aieval run [--out 报告.json] [--record cassette.json | --replay cassette.json]
//	aieval replay cassette.json [--out 报告.json]
//	aieval compare 旧报告.json 新报告.json [--allow-different-cases]
//
// run 未指定 --replay 时读取 AI_API_KEY、AI_BASE_URL、AI_MODEL、AI_EXTRA_HEADERS，与 salon serve 相同；
// 数据库使用 dbtest（SALON_TEST_PG_DSN 或 testcontainers），不连接开发库。
// OpenCode Go 网关（https://opencode.ai/inference/go/openai/v1）需要 AI_EXTRA_HEADERS 提供
// {"x-opencode-session":"salon"}；未设置时不会根据 base URL 自动猜测，报告也不记录头的名称或值。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ai/eval"
)

const usage = "usage: aieval run [--out 报告.json] [--record cassette.json | --replay cassette.json] | aieval replay cassette.json [--out 报告.json] | aieval compare 旧报告.json 新报告.json [--allow-different-cases]"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "aieval:", err)
		os.Exit(1)
	}
}

func run(args []string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		out := fs.String("out", "", "写入 JSON 报告")
		record := fs.String("record", "", "把模型响应记录为 cassette")
		replay := fs.String("replay", "", "离线重放 cassette，不访问模型")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New(usage)
		}
		if *record != "" && *replay != "" {
			return errors.New("--record 和 --replay 不能同时使用")
		}
		return runSuite(*out, *record, *replay)
	case "replay":
		cassette, out, err := replayArgs(args[1:])
		if err != nil {
			return err
		}
		return runSuite(out, "", cassette)
	case "compare":
		files, allow, err := compareArgs(args[1:])
		if err != nil {
			return err
		}
		return compareReports(files[0], files[1], allow)
	default:
		return errors.New(usage)
	}
}

func runSuite(outPath, recordPath, replayPath string) error {
	suite, err := eval.LoadSuite()
	if err != nil {
		return err
	}
	modelName := envOr("AI_MODEL", ai.DefaultModel)
	baseURL := envOr("AI_BASE_URL", ai.DefaultBaseURL)
	host := hostOnly(baseURL)
	var factory eval.ModelFactory
	var recorder *eval.Recorder
	var replay *eval.Replay
	switch {
	case replayPath != "":
		cassette, err := eval.LoadCassette(replayPath)
		if err != nil {
			return err
		}
		if cassette.CaseFileHash != suite.Hash {
			return fmt.Errorf("cassette 的用例文件哈希 %s 与当前用例 %s 不同，请重新录制", cassette.CaseFileHash, suite.Hash)
		}
		replay = eval.NewReplay(cassette)
		modelName = "replay"
		host = "offline"
		factory = func(c eval.Case, f *eval.Fixture) ai.Model { return replay.Model(c, f) }
	default:
		cfg, err := ai.OpenAIConfigFromEnv()
		if err != nil {
			return err
		}
		if cfg.APIKey == "" {
			return errors.New("AI_API_KEY 未设置；请配置后用 aieval run，或使用 --replay 离线重放")
		}
		model := ai.NewOpenAIModel(cfg)
		factory = func(_ eval.Case, _ *eval.Fixture) ai.Model { return model }
		if recordPath != "" {
			recorder = eval.NewRecorder()
			factory = func(c eval.Case, f *eval.Fixture) ai.Model {
				return recorder.Model(c, f, model)
			}
		}
	}
	tb := eval.NewCLITB()
	report, err := eval.Run(context.Background(), tb, suite, eval.RunConfig{
		ModelName: modelName, BaseURLHost: host, GitCommit: eval.GitCommit(), Now: time.Now,
	}, factory)
	if err != nil {
		tb.RunCleanups()
		return err
	}
	if err := checkCleanup(tb); err != nil {
		return err
	}
	if outPath != "" {
		if err := writeJSON(outPath, report); err != nil {
			return err
		}
	}
	if recorder != nil && recordPath != "" {
		if err := eval.SaveCassette(recordPath, recorder.Cassette(suite.Hash)); err != nil {
			return err
		}
	}
	printReport(report)
	return nil
}

func compareReports(oldPath, newPath string, allowDifferent bool) error {
	var oldReport, newReport eval.Report
	if err := readJSON(oldPath, &oldReport); err != nil {
		return err
	}
	if err := readJSON(newPath, &newReport); err != nil {
		return err
	}
	comparison, err := eval.Compare(oldReport, newReport, allowDifferent)
	if err != nil {
		return err
	}
	fmt.Printf("比较：%s → %s\n", oldPath, newPath)
	fmt.Printf("用例文件：%s → %s\n", shortHash(comparison.OldCaseFileHash), shortHash(comparison.NewCaseFileHash))
	fmt.Println("\n指标")
	for _, metric := range comparison.Metrics {
		fmt.Printf("  %-22s %6.1f%% → %6.1f%%  Δ%+6.1f  (%d → %d)\n",
			metric.Name, metric.Old*100, metric.New*100, metric.Delta*100, metric.OldCount, metric.NewCount)
	}
	fmt.Println("\n通过状态变化")
	if len(comparison.Flips) == 0 {
		fmt.Println("  没有通过状态变化的用例")
	}
	for _, flip := range comparison.Flips {
		fmt.Printf("  %-28s %-6s → %-6s  %s\n", flip.ID, flip.Before, flip.After, flip.Story)
	}
	if len(comparison.OnlyOld) > 0 {
		fmt.Printf("\n只在旧报告：%s\n", strings.Join(comparison.OnlyOld, ", "))
	}
	if len(comparison.OnlyNew) > 0 {
		fmt.Printf("\n只在新报告：%s\n", strings.Join(comparison.OnlyNew, ", "))
	}
	return nil
}

func printReport(report eval.Report) {
	meta := report.Run
	fmt.Printf("AI 评测：%s（%s）\n", meta.Model, meta.BaseURLHost)
	fmt.Printf("用例 %d 条，提示词指纹 %s，用例文件 %s\n", meta.CaseCount, shortHash(meta.PromptHash), shortHash(meta.CaseFileHash))
	fmt.Printf("%-30s %-7s %-7s %-7s %-7s %-7s\n", "用例", "工具", "参数", "回答", "工具成功", "通过")
	for _, result := range report.Cases {
		fmt.Printf("%-30s %-7s %-7s %-7s %-7s %-7s\n", result.ID,
			passMark(result.ToolSelection), passMark(result.Arguments), passMark(result.Answer), passMark(result.ToolsOK), passMark(result.Overall))
	}
	fmt.Println("\n按故事")
	fmt.Printf("%-28s %-5s %-7s %-7s %-7s %-7s\n", "故事", "用例", "工具", "参数", "回答", "通过")
	printAggregates(report.Aggregates)
	fmt.Printf("\n总体：工具 %.1f%%  参数(调用) %.1f%%  参数(用例) %.1f%%  回答 %.1f%%  工具成功 %.1f%%  通过 %.1f%%\n",
		report.Aggregates.Overall.ToolSelectionAccuracy*100, report.Aggregates.Overall.ArgumentCallAccuracy*100,
		report.Aggregates.Overall.ArgumentCaseAccuracy*100, report.Aggregates.Overall.AnswerAccuracy*100,
		report.Aggregates.Overall.ToolSuccessAccuracy*100, report.Aggregates.Overall.OverallAccuracy*100)
	if report.KnownGaps.Overall.Cases > 0 {
		fmt.Printf("\n已知缺口（单独统计）：%d 条，通过率 %.1f%%\n", report.KnownGaps.Overall.Cases, report.KnownGaps.Overall.OverallAccuracy*100)
	}
}

func printAggregates(aggregates eval.Aggregates) {
	stories := make([]string, 0, len(aggregates.ByStory))
	for story := range aggregates.ByStory {
		stories = append(stories, story)
	}
	sort.Strings(stories)
	for _, story := range stories {
		metrics := aggregates.ByStory[story]
		fmt.Printf("%-28s %-5d %-7s %-7s %-7s %-7s\n", story, metrics.Cases,
			percent(metrics.ToolSelectionAccuracy), percent(metrics.ArgumentCallAccuracy), percent(metrics.AnswerAccuracy), percent(metrics.OverallAccuracy))
	}
}

func passMark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func percent(value float64) string { return fmt.Sprintf("%.1f%%", value*100) }

func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func hostOnly(baseURL string) string {
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func checkCleanup(tb *eval.CLITB) error {
	tb.RunCleanups()
	if err := cleanupErrors(tb); err != nil {
		return err
	}
	return nil
}

func cleanupErrors(tb *eval.CLITB) error {
	errs := tb.Errors()
	if len(errs) == 0 {
		return nil
	}
	for _, message := range errs {
		fmt.Fprintln(os.Stderr, "cleanup:", message)
	}
	return fmt.Errorf("清理失败：%s", strings.Join(errs, "; "))
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func readJSON(path string, value any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func replayArgs(args []string) (cassette, out string, err error) {
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--out":
			if i+1 >= len(args) {
				return "", "", errors.New("--out 需要文件路径")
			}
			i++
			out = args[i]
		case strings.HasPrefix(arg, "--out="):
			out = strings.TrimPrefix(arg, "--out=")
		case strings.HasPrefix(arg, "-"):
			return "", "", fmt.Errorf("未知参数 %s", arg)
		case cassette == "":
			cassette = arg
		default:
			return "", "", errors.New("usage: aieval replay cassette.json [--out 报告.json]")
		}
	}
	if cassette == "" {
		return "", "", errors.New("usage: aieval replay cassette.json [--out 报告.json]")
	}
	return cassette, out, nil
}

func compareArgs(args []string) (files []string, allow bool, err error) {
	for _, arg := range args {
		switch {
		case arg == "--allow-different-cases":
			allow = true
		case strings.HasPrefix(arg, "-"):
			return nil, false, fmt.Errorf("未知参数 %s", arg)
		default:
			files = append(files, arg)
		}
	}
	if len(files) != 2 {
		return nil, false, errors.New("usage: aieval compare 旧报告.json 新报告.json [--allow-different-cases]")
	}
	return files, allow, nil
}
