package storeai_test

import (
	"context"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ai"
	"github.com/wzhlinhai/salon/backend/internal/ai/eval"
	"github.com/wzhlinhai/salon/backend/internal/storeai"
	storeaieval "github.com/wzhlinhai/salon/backend/internal/storeai/eval"
)

var agentCassettePath = "testdata/agent.cassette.json"

func TestAgentOracleScoresPerfect(t *testing.T) {
	suite, err := storeai.LoadAgentSuite()
	if err != nil {
		t.Fatal(err)
	}
	recorder := storeai.NewEvalRecorder()
	factory := func(c storeai.AgentCase, f *eval.Fixture) ai.Model {
		return recorder.Model(c.ID, f.Refs, &storeaieval.OracleAgent{Case: c, Fixture: f})
	}
	report, err := storeaieval.RunAgentSuite(context.Background(), t, suite, "oracle", "offline", factory)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range report.Cases {
		if result.Error != "" || !result.Hit {
			t.Errorf("%s: %+v", result.ID, result)
		}
	}
	if report.Pass != report.Total || report.Accuracy != 1 {
		t.Fatalf("report = %+v", report)
	}
	cassette := recorder.Cassette(suite.Hash)
	if *updateClassifierCassette {
		if err := storeai.SaveEvalCassette(agentCassettePath, cassette); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := storeai.LoadEvalCassette(agentCassettePath); err != nil {
		t.Fatalf("load agent cassette: %v (run go test ./internal/storeai -run TestAgentOracleScoresPerfect -update-cassette)", err)
	}
}

func TestAgentCassetteReplays(t *testing.T) {
	suite, err := storeai.LoadAgentSuite()
	if err != nil {
		t.Fatal(err)
	}
	cassette, err := storeai.LoadEvalCassette(agentCassettePath)
	if err != nil {
		t.Fatalf("load agent cassette: %v (run go test ./internal/storeai -run TestAgentOracleScoresPerfect -update-cassette)", err)
	}
	if cassette.CaseFileHash != suite.Hash {
		t.Fatalf("cassette 的用例文件哈希不同：%s != %s（run go test ./internal/storeai -run TestAgentOracleScoresPerfect -update-cassette）", cassette.CaseFileHash, suite.Hash)
	}
	replay := storeai.NewEvalReplay(cassette)
	models := map[string]ai.Model{}
	factory := func(c storeai.AgentCase, f *eval.Fixture) ai.Model {
		model := replay.Model(c.ID, f.Refs)
		models[c.ID] = model
		return model
	}
	report, err := storeaieval.RunAgentSuite(context.Background(), t, suite, "replay", "offline", factory)
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
