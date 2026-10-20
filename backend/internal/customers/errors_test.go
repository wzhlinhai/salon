package customers

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestUniqueViolationMessages(t *testing.T) {
	for _, tc := range []struct{ constraint, message string }{
		{"customer_sources_store_id_name_key", "顾客来源名称已存在"},
		{"customers_unique_phone", "这个手机号已属于另一位顾客，可以合并这两位顾客"},
	} {
		original := &pgconn.PgError{Code: "23505", ConstraintName: tc.constraint}
		err := rowError(original)
		var rule *ops.RuleError
		if !errors.As(err, &rule) || rule.Message != tc.message {
			t.Fatalf("%s: %v, want %s", tc.constraint, err, tc.message)
		}
	}
	for _, err := range []error{&pgconn.PgError{Code: "23505", ConstraintName: "unknown"}, &pgconn.PgError{Code: "23503"}} {
		if rowError(err) != err {
			t.Fatalf("unexpectedly translated %v", err)
		}
	}
}
