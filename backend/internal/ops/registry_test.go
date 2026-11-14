package ops_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/wzhlinhai/salon/backend/internal/ops"
)

type in struct{}
type out struct{}

func handle(context.Context, ops.Env, *in) (*out, error) { return &out{}, nil }

func newRegistry(t *testing.T) *ops.Registry {
	_, api := humatest.New(t)
	return ops.NewRegistry(nil, api, time.Now)
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

type defaultFields struct {
	Kind string `json:"kind,omitempty" default:"service"`
}

type recursiveDefaultInput struct {
	Next   *recursiveDefaultInput `json:"next,omitempty"`
	Fields defaultFields          `json:"fields"`
}

func registerInput[I any](t *testing.T) {
	t.Helper()
	ops.Register(newRegistry(t), ops.Operation[I, out]{
		Name: "do_thing", Summary: "示例", Kind: ops.Read, Permission: "thing.view", AI: ops.AIDirect,
		Handle: func(context.Context, ops.Env, *I) (*out, error) { return &out{}, nil },
	})
}

func TestInputDefaultsPanicAtRegistration(t *testing.T) {
	cases := map[string]struct {
		register func(*testing.T)
		path     string
	}{
		"top level":  {registerInput[defaultFields], "Kind"},
		"embedded":   {registerInput[struct{ defaultFields }], "defaultFields.Kind"},
		"pointer":    {registerInput[struct{ Fields *defaultFields }], "Fields.Kind"},
		"slice":      {registerInput[struct{ Fields []defaultFields }], "Fields[].Kind"},
		"array":      {registerInput[struct{ Fields [2]defaultFields }], "Fields[].Kind"},
		"map values": {registerInput[struct{ Fields map[string]*defaultFields }], "Fields[value].Kind"},
		"recursive":  {registerInput[recursiveDefaultInput], "Fields.Kind"},
		"empty default": {registerInput[struct {
			Kind string `default:""`
		}], "Kind"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			want := `ops: invalid operation "do_thing": field ` + c.path + ": default must be set in the handler because the AI path does not apply huma defaults"
			assertPanics(t, want, func() { c.register(t) })
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

	caller := ops.Caller{Permissions: map[ops.Permission]ops.Grant{"thing.view": {}}}
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

func TestReadToolsExcludeWritesAndDrafts(t *testing.T) {
	r := newRegistry(t)
	read := valid()
	read.Name = "read_thing"
	ops.Register(r, read)
	draft := valid()
	draft.Name, draft.Kind, draft.AI = "draft_thing", ops.Write, ops.AIDraft
	ops.Register(r, draft)
	control := valid()
	control.Name, control.Kind, control.AI = "create_draft", ops.Write, ops.AIManageDraft
	ops.Register(r, control)

	caller := ops.Caller{Permissions: map[ops.Permission]ops.Grant{"thing.view": {}}}
	tools := r.ReadTools(caller)
	if len(tools) != 1 || tools[0].Name != "read_thing" {
		t.Fatalf("ReadTools = %v, want only read_thing", tools)
	}
	for _, name := range []string{"draft_thing", "create_draft", "no_such_tool"} {
		if _, err := r.CallReadTool(context.Background(), caller, name, nil); !errors.Is(err, ops.ErrUnknownOperation) {
			t.Fatalf("CallReadTool(%q) err = %v, want ErrUnknownOperation", name, err)
		}
	}
}

func TestReadToolsIncludeEveryDirectRead(t *testing.T) {
	r := newRegistry(t)
	names := []string{"read_thing", "list_drafts", "get_draft", "get_redemption", "get_rotation"}
	for _, name := range names {
		op := valid()
		op.Name = name
		ops.Register(r, op)
	}
	caller := ops.Caller{Permissions: map[ops.Permission]ops.Grant{"thing.view": {}}}
	tools := r.ReadTools(caller)
	if len(tools) != len(names) {
		t.Fatalf("ReadTools = %v, want %d reads", tools, len(names))
	}
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = true
	}
	for _, name := range names {
		if !got[name] {
			t.Fatalf("ReadTools missing %s: %v", name, tools)
		}
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

func TestDraftControlModeIsReserved(t *testing.T) {
	op := valid()
	op.AI = ops.AIManageDraft
	assertPanics(t, "draft control mode is reserved", func() { ops.Register(newRegistry(t), op) })
	op.Name = "create_draft"
	assertPanics(t, "draft control mode is reserved", func() { ops.Register(newRegistry(t), op) })
	op.Kind = ops.Write
	ops.Register(newRegistry(t), op)
}

func TestDraftToolSchemaAndValidation(t *testing.T) {
	r := newRegistry(t)
	ops.Register(r, ops.Operation[struct {
		Name string `json:"name" minLength:"1"`
	}, out]{Name: "new_thing", Summary: "创建", Kind: ops.Write, Permission: "thing.edit", AI: ops.AIDraft, Handle: func(context.Context, ops.Env, *struct {
		Name string `json:"name" minLength:"1"`
	}) (*out, error) {
		t.Fatal("business handler called by AI")
		return nil, nil
	}})
	c := ops.Caller{Permissions: map[ops.Permission]ops.Grant{"thing.edit": {}}}
	tools := r.Tools(c)
	if len(tools) != 1 || !tools[0].Draft || !strings.Contains(tools[0].Description, "只创建草稿") {
		t.Fatal(tools)
	}
	props := tools[0].Parameters["properties"].(map[string]any)
	for _, key := range []string{"name", "summary", "utterance"} {
		if props[key] == nil {
			t.Fatal(key, props)
		}
	}
	// 无数据库也必须先返回 schema 错误，不能进入事务。
	_, err := r.CallTool(context.Background(), c, "new_thing", []byte(`{"summary":"草稿"}`))
	if ops.StatusOf(err) != 422 || !strings.Contains(err.Error(), "name") {
		t.Fatal(err)
	}
	_, err = r.CallTool(context.Background(), c, "new_thing", []byte(`{"name":"a","utterance":3}`))
	if ops.StatusOf(err) != 422 || !strings.Contains(err.Error(), "utterance must be a string") {
		t.Fatal(err)
	}
}
