package storeai_test

import (
	"context"
	"flag"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
	storeaieval "github.com/wzhlinhai/salon/backend/internal/storeai/eval"
)

var (
	updateClassifierCassette = flag.Bool("update-cassette", false, "用 oracle 重写 testdata/classifier.cassette.json")
	classifierCassettePath   = "testdata/classifier.cassette.json"
)

func TestClassifierOracleScoresPerfect(t *testing.T) {
	suite, err := storeai.LoadClassifierSuite()
	if err != nil {
		t.Fatal(err)
	}
	recorder := storeai.NewEvalRecorder()
	factory := func(c storeai.ClassifierCase, refs map[string]string) ai.Model {
		return recorder.Model(c.ID, refs, &storeaieval.OracleClassifier{Case: c})
	}
	report, err := storeaieval.RunClassifierSuite(context.Background(), t, suite, "oracle", "offline", factory)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Cases {
		if result.Error != "" || !result.Hit {
			t.Errorf("%s: %+v", result.ID, result)
		}
		if result.ExpectedSource == storeai.ClassifierRule && result.ModelCalls != 0 {
			t.Errorf("%s: rule case called the model %d times", result.ID, result.ModelCalls)
		}
	}
	if report.Pass != report.Total || report.Accuracy != 1 {
		t.Fatalf("report = %+v", report)
	}
	cassette := recorder.Cassette(suite.Hash)
	if *updateClassifierCassette {
		if err := storeai.SaveEvalCassette(classifierCassettePath, cassette); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := storeai.LoadEvalCassette(classifierCassettePath); err != nil {
		t.Fatalf("load classifier cassette: %v (run go test ./internal/storeai -run TestClassifierOracleScoresPerfect -update-cassette)", err)
	}
}

func TestClassifierCassetteReplays(t *testing.T) {
	suite, err := storeai.LoadClassifierSuite()
	if err != nil {
		t.Fatal(err)
	}
	cassette, err := storeai.LoadEvalCassette(classifierCassettePath)
	if err != nil {
		t.Fatalf("load classifier cassette: %v (run go test ./internal/storeai -run TestClassifierOracleScoresPerfect -update-cassette)", err)
	}
	if cassette.CaseFileHash != suite.Hash {
		t.Fatalf("cassette 的用例文件哈希不同：%s != %s（run go test ./internal/storeai -run TestClassifierOracleScoresPerfect -update-cassette）", cassette.CaseFileHash, suite.Hash)
	}
	replay := storeai.NewEvalReplay(cassette)
	models := map[string]ai.Model{}
	factory := func(c storeai.ClassifierCase, refs map[string]string) ai.Model {
		model := replay.Model(c.ID, refs)
		models[c.ID] = model
		return model
	}
	report, err := storeaieval.RunClassifierSuite(context.Background(), t, suite, "replay", "offline", factory)
	if err != nil {
		t.Fatal(err)
	}
	for id, model := range models {
		if finisher, ok := model.(interface{ Finish() error }); ok {
			if err := finisher.Finish(); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
		}
	}
	for _, result := range report.Cases {
		if result.Error != "" || !result.Hit {
			t.Errorf("%s: %+v", result.ID, result)
		}
	}
	if report.Pass != report.Total {
		t.Fatalf("report = %+v", report)
	}
}
