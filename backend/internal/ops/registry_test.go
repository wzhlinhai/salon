package ops_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type in struct{}
type out struct{}

func handle(context.Context, ops.Env, *in) (*out, error) { return &out{}, nil }

func newRegistry(t *testing.T) *ops.Registry {
	_, api := humatest.New(t)
	return ops.NewRegistry(nil, api)
}

func valid() ops.Operation[in, out] {
	return ops.Operation[in, out]{
		Name: "do_thing", Summary: "示例", Kind: ops.Read, Permission: "thing.view", AI: ops.AIDirect, Handle: handle,
	}
}

func TestInvalidDeclarationsPanicAtRegistration(t *testing.T) {
	cases := map[string]struct {
		mutate func(*ops.Operation[in, out])
		want   string
	}{
		"name not usable as AI tool name": {func(o *ops.Operation[in, out]) { o.Name = "do.thing" }, "name must match"},
		"missing permission":              {func(o *ops.Operation[in, out]) { o.Permission = "" }, "permission is required"},
		"missing kind":                    {func(o *ops.Operation[in, out]) { o.Kind = 0 }, "kind must be"},
		"missing AI mode":                 {func(o *ops.Operation[in, out]) { o.AI = 0 }, "AI mode is required"},
		"write executed by AI directly":   {func(o *ops.Operation[in, out]) { o.Kind = ops.Write }, "cannot be executed by AI directly"},
		"missing handler":                 {func(o *ops.Operation[in, out]) { o.Handle = nil }, "handler is required"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			op := valid()
			c.mutate(&op)
			assertPanics(t, c.want, func() { ops.Register(newRegistry(t), op) })
		})
	}
}

func TestDuplicateNamePanics(t *testing.T) {
	r := newRegistry(t)
	ops.Register(r, valid())
	assertPanics(t, "duplicate operation", func() { ops.Register(r, valid()) })
}

func TestWriteOperationsMayBeDraftOrHiddenFromAI(t *testing.T) {
	r := newRegistry(t)
	for name, mode := range map[string]ops.AIMode{"draft_write": ops.AIDraft, "hidden_write": ops.AINone} {
		op := valid()
		op.Name, op.Kind, op.AI = name, ops.Write, mode
		ops.Register(r, op)
	}
}

func TestOperationsClosedToAIAreNotTools(t *testing.T) {
	r := newRegistry(t)
	op := valid()
	op.Name, op.Kind, op.AI = "redeem_voucher", ops.Write, ops.AINone
	ops.Register(r, op)

	caller := ops.Caller{Permissions: []ops.Permission{"thing.view"}}
	if tools := r.Tools(caller); len(tools) != 0 {
		t.Fatalf("tools = %v, want none", tools)
	}
	if _, err := r.CallTool(context.Background(), caller, "redeem_voucher", nil); !errors.Is(err, ops.ErrUnknownOperation) {
		t.Fatalf("CallTool err = %v, want ErrUnknownOperation", err)
	}
	if _, err := r.CallTool(context.Background(), caller, "no_such_tool", nil); !errors.Is(err, ops.ErrUnknownOperation) {
		t.Fatalf("CallTool unknown err = %v, want ErrUnknownOperation", err)
	}
}

func TestPermissionsListsDeclaredPermissions(t *testing.T) {
	r := newRegistry(t)
	for _, d := range []struct{ name, perm string }{{"b_op", "b.view"}, {"a_op", "a.view"}, {"a_op2", "a.view"}} {
		op := valid()
		op.Name, op.Permission = d.name, ops.Permission(d.perm)
		ops.Register(r, op)
	}
	got := r.Permissions()
	if len(got) != 2 || got[0] != "a.view" || got[1] != "b.view" {
		t.Fatalf("Permissions() = %v, want [a.view b.view]", got)
	}
}

func assertPanics(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatalf("did not panic, want %q", want)
		}
		if msg, _ := r.(string); !strings.Contains(msg, want) {
			t.Fatalf("panic = %v, want it to contain %q", r, want)
		}
	}()
	f()
}
