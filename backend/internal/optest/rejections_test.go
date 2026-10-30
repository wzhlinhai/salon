package optest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func rejectionStatus(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.INT {
			n, err := strconv.ParseInt(e.Value, 0, 64)
			return err == nil && (n == 403 || n == 409)
		}
	case *ast.SelectorExpr:
		id, ok := e.X.(*ast.Ident)
		return ok && id.Name == "http" && (e.Sel.Name == "StatusConflict" || e.Sel.Name == "StatusForbidden")
	}
	return false
}

func rejectionSubject(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name == "Status" || e.Sel.Name == "Code"
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok {
			id, ok := sel.X.(*ast.Ident)
			return ok && id.Name == "ops" && sel.Sel.Name == "StatusOf"
		}
	}
	return false
}

func rejectionCompanion(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
				if sel.Sel.Name == "Detail" || sel.Sel.Name == "Error" {
					found = true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "errors" && sel.Sel.Name == "Is" {
					found = true
				}
			}
		case *ast.IndexExpr:
			if key, ok := n.Index.(*ast.BasicLit); ok && key.Kind == token.STRING {
				value, err := strconv.Unquote(key.Value)
				if err == nil && value == "detail" {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

func rejectionLines(fset *token.FileSet, file *ast.File) []int {
	exemptions := map[int]bool{}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !strings.HasPrefix(comment.Text, "//") {
				continue
			}
			_, reason, ok := strings.Cut(comment.Text, "rejection-detail:")
			if ok && strings.TrimSpace(reason) != "" {
				exemptions[fset.Position(comment.Pos()).Line] = true
			}
		}
	}
	var lines []int
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		violation := false
		switch n := n.(type) {
		case *ast.ExprStmt:
			if call, ok := n.X.(*ast.CallExpr); ok && len(call.Args) > 0 {
				violation = rejectionStatus(call.Args[len(call.Args)-1])
			}
		case *ast.BinaryExpr:
			if (n.Op == token.EQL || n.Op == token.NEQ) && ((rejectionSubject(n.X) && rejectionStatus(n.Y)) || (rejectionSubject(n.Y) && rejectionStatus(n.X))) {
				for i := len(stack) - 1; i >= 0; i-- {
					if parent, ok := stack[i].(*ast.IfStmt); ok {
						violation = !rejectionCompanion(parent.Cond)
						break
					}
				}
			}
		}
		line := fset.Position(n.Pos()).Line
		if violation && !exemptions[line] && !exemptions[line-1] {
			lines = append(lines, line)
		}
		stack = append(stack, n)
		return true
	})
	return lines
}

func TestRejectionAssertions(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("cannot find backend/go.mod")
		}
		root = parent
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, line := range rejectionLines(fset, file) {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			t.Errorf("%s:%d: assert the rule's text (or errors.Is) next to the status, or add // rejection-detail: <reason>", relative, line)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRejectionDetector(t *testing.T) {
	cases := []struct {
		name, source string
		want         []int
	}{
		{"discard literal", `s.call("x", in, 409)`, []int{3}},
		{"discard constant", `optest.WaitResult(t, done, http.StatusForbidden)`, []int{3}},
		{"assigned", `r := s.call("x", in, 403)`, nil},
		{"status", `if r.Status != 409 { fail() }`, []int{3}},
		{"code", `if r.Code == http.StatusForbidden { fail() }`, []int{3}},
		{"error status", `if ops.StatusOf(err) != 403 { fail() }`, []int{3}},
		{"detail", `if r.Status != 409 || r.Detail() != "rule" { fail() }`, nil},
		{"map", `if r.Code != 403 || r["detail"] != "rule" { fail() }`, nil},
		{"error", `if ops.StatusOf(err) != 409 || err.Error() != "rule" { fail() }`, nil},
		{"sentinel", `if ops.StatusOf(err) != 403 || !errors.Is(err, sentinel) { fail() }`, nil},
		{"nonliteral", `if r.Status != tc.want { fail() }`, nil},
		{"same line exemption", "s.call(403) // rejection-detail: status mapping", nil},
		{"previous line exemption", "// rejection-detail: status mapping\nif r.Code != 409 { fail() }", nil},
		{"empty exemption", "s.call(403) // rejection-detail: ", []int{3}},
		{"distant exemption", "// rejection-detail: status mapping\n\ns.call(403)", []int{5}},
		{"body does not count", `if r.Status != 409 { r.Detail() }`, []int{3}},
		{"nearest if", `if r.Detail() == "rule" { if r.Status != 409 { fail() } }`, []int{3}},
		{"whole condition", `if (r.Status != 409 || other) && r.Detail() != "rule" { fail() }`, nil},
		{"other field", `if r.Count != 409 { fail() }`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "fixture.go", "package fixture\nfunc test() {\n"+tc.source+"\n}", parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			if got := rejectionLines(fset, file); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lines = %v, want %v", got, tc.want)
			}
		})
	}
}
