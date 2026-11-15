package miniprogram

import (
	"errors"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestParseSlotDateRejectsInvalid(t *testing.T) {
	_, err := parseSlotDate("2026/06/12")
	var rule *ops.RuleError
	if !errors.As(err, &rule) || rule.Message != "日期格式不正确" {
		t.Fatalf("err = %v", err)
	}
}
