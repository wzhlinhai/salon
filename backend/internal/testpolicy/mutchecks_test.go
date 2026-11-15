package testpolicy

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runMutchecksTest 在仓库根目录运行一个 mutchecks 工具测试。
func runMutchecksTest(t *testing.T, pattern string) {
	t.Helper()
	command := exec.Command("python3", "-m", "unittest", "backend/scripts/test_mutchecks.py", "-k", pattern)
	command.Dir = filepath.Join("..", "..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("mutchecks test %s failed: %v\n%s", pattern, err, output)
	}
	if !strings.Contains(string(output), "Ran 1 test") {
		t.Fatalf("mutchecks test %s did not run exactly one test:\n%s", pattern, output)
	}
}

func TestMutchecksE2EMutatedResetIsEnvironment(t *testing.T) {
	runMutchecksTest(t, "test_e2e_mutated_reset_failure_is_environment")
}

func TestMutchecksE2ERestoredResetIsEnvironment(t *testing.T) {
	runMutchecksTest(t, "test_e2e_restored_reset_failure_is_environment")
}

func TestMutchecksE2ERestoredResetWithoutCatchFails(t *testing.T) {
	runMutchecksTest(t, "test_e2e_restored_reset_without_catch_fails")
}

func TestMutchecksE2ERestoredResetPartialCatchFails(t *testing.T) {
	runMutchecksTest(t, "test_e2e_restored_reset_partial_catch_fails")
}

func TestMutchecksE2ERestoreFailureAfterEnvironmentFails(t *testing.T) {
	runMutchecksTest(t, "test_e2e_restore_failure_after_environment_fails")
}

func TestMutchecksE2EEnvironmentNeverCountsAsPassed(t *testing.T) {
	runMutchecksTest(t, "test_e2e_environment_never_counts_as_passed")
}

func TestMutchecksE2EExitFourWithoutResetMarkerFails(t *testing.T) {
	runMutchecksTest(t, "test_e2e_exit_four_without_reset_marker_fails")
}
