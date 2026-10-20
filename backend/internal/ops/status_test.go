package ops_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

func TestStatusOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"参数错误", &ops.ToolError{Messages: []string{"name is required"}}, http.StatusUnprocessableEntity},
		{"业务规则", ops.Rule("至少要保留一名在职老板"), http.StatusConflict},
		{"需要草稿", ops.ErrDraftRequired, http.StatusConflict},
		{"未登录", ops.ErrUnauthenticated, http.StatusUnauthorized},
		{"无权限", ops.ErrForbidden, http.StatusForbidden},
		{"不存在", ops.ErrNotFound, http.StatusNotFound},
		{"未知操作", ops.ErrUnknownOperation, http.StatusNotFound},
		{"包装后的错误", fmt.Errorf("load staff: %w", ops.ErrForbidden), http.StatusForbidden},
		{"其他错误", errors.New("connection reset"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := ops.StatusOf(c.err); got != c.want {
			t.Errorf("%s: StatusOf = %d, want %d", c.name, got, c.want)
		}
	}
}
