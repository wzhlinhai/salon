package miniprogram

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/wzhlinhai/salon/backend/internal/db/dbgen"
	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestCustomerLoginStoreCheckMessage(t *testing.T) {
	customer := dbgen.Customer{StoreID: uuid.New()}
	err := ensureLoginStore(customer, uuid.New())
	var rule *ops.RuleError
	if !errors.As(err, &rule) || rule.Message != "该顾客属于其他门店，不能登录" {
		t.Fatalf("err = %v", err)
	}
}
