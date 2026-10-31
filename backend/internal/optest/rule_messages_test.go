package optest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type ruleMessage struct {
	line int
	text string
}

func rulePrefix(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		if expr.Kind == token.STRING {
			value, _ := strconv.Unquote(expr.Value)
			return value
		}
	case *ast.BinaryExpr:
		if expr.Op == token.ADD {
			return rulePrefix(expr.X)
		}
	case *ast.ParenExpr:
		return rulePrefix(expr.X)
	}
	return ""
}

func missingRuleMessages(fset *token.FileSet, file *ast.File, tests []string) ([]ruleMessage, error) {
	opsName := ""
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		if path == "github.com/wzhlinhai/salon/backend/internal/ops" {
			opsName = "ops"
			if imp.Name != nil {
				opsName = imp.Name.Name
			}
			if opsName == "." {
				return nil, fmt.Errorf("%s: unsupported ops.Rule form: dot import", fset.Position(imp.Pos()))
			}
		}
	}
	calls := map[*ast.SelectorExpr]*ast.CallExpr{}
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				calls[sel] = call
			}
		}
		return true
	})
	var unsupported error
	exemptions := map[int]bool{}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if reason, ok := strings.CutPrefix(comment.Text, "// rule-untested:"); ok && strings.TrimSpace(reason) != "" {
				exemptions[fset.Position(comment.Pos()).Line+1] = true
			}
		}
	}
	var missing []ruleMessage
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Rule" {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || opsName == "" || id.Name != opsName {
			return true
		}
		call, ok := calls[sel]
		if !ok {
			unsupported = fmt.Errorf("%s: unsupported ops.Rule form: function value", fset.Position(sel.Pos()))
			return true
		}
		if len(call.Args) != 1 {
			return true
		}
		message := rulePrefix(call.Args[0])
		line := fset.Position(call.Pos()).Line
		if message != "" && !exemptions[line] && !ruleCovered(tests, message) {
			missing = append(missing, ruleMessage{line, message})
		}
		return true
	})
	return missing, unsupported
}

func ruleCovered(tests []string, message string) bool {
	for _, literal := range tests {
		if strings.Contains(literal, message) {
			return true
		}
	}
	return false
}

func testLiterals(path string, file *ast.File) []string {
	if filepath.Base(path) == "rule_messages_test.go" && filepath.Base(filepath.Dir(path)) == "optest" {
		return nil
	}
	var literals []string
	ast.Inspect(file, func(n ast.Node) bool {
		if literal, ok := n.(*ast.BasicLit); ok && literal.Kind == token.STRING {
			value, _ := strconv.Unquote(literal.Value)
			literals = append(literals, value)
		}
		return true
	})
	return literals
}

func TestRuleMessages(t *testing.T) {
	root := ".."
	var tests []string
	var sources []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			tests = append(tests, testLiterals(path, file)...)
		} else {
			sources = append(sources, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range sources {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		missing, err := missingRuleMessages(fset, file, tests)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range missing {
			t.Errorf("%s:%d: ops.Rule message has no test: %s", path, message.line, message.text)
		}
	}
}

func TestRuleMessageDetector(t *testing.T) {
	cases := []struct {
		name, source, tests string
		want                int
	}{
		{"literal found", `ops.Rule("规则")`, `r.Detail() != "规则"`, 0},
		{"literal missing", `ops.Rule("规则")`, "", 1},
		{"concatenation prefix", `ops.Rule("权限重复：" + name + suffix)`, `"权限重复：abc"`, 0},
		{"missing prefix", `ops.Rule("权限重复：" + name)`, "", 1},
		{"exemption", "// rule-untested: schema rejects input\nops.Rule(\"规则\")", "", 0},
		{"empty exemption", "// rule-untested: \nops.Rule(\"规则\")", "", 1},
		{"distant exemption", "// rule-untested: schema rejects input\n\nops.Rule(\"规则\")", "", 1},
		{"same line exemption", "ops.Rule(\"规则\") // rule-untested: schema", "", 1},
		{"nonliteral", `ops.Rule(message)`, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "fixture.go", `package fixture
import "github.com/wzhlinhai/salon/backend/internal/ops"
func test() {
`+tc.source+"\n}", parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			got, err := missingRuleMessages(fset, file, []string{tc.tests})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("missing = %v, want %d", got, tc.want)
			}
			if len(got) > 0 && (got[0].line < 3 || got[0].text == "") {
				t.Fatalf("missing location/message: %v", got)
			}
		})
	}
}

func TestRuleMessageCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, path, source string
		want               int
	}{
		{"comment only", "cards/example_test.go", "// 规则\n", 1},
		{"checker only", "optest/rule_messages_test.go", `var fixture = "规则"`, 1},
		{"test literal", "cards/example_test.go", "var detail = `前缀规则后缀`", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, tc.path, "package fixture\n"+tc.source, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			literals := testLiterals(tc.path, file)
			source, err := parser.ParseFile(fset, "source.go", `package fixture
import "github.com/wzhlinhai/salon/backend/internal/ops"
func reject() { ops.Rule("规则") }
`, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			missing, err := missingRuleMessages(fset, source, literals)
			if err != nil || len(missing) != tc.want {
				t.Fatalf("missing=%v err=%v, want %d", missing, err, tc.want)
			}
		})
	}
}

func TestRuleMessageImportForms(t *testing.T) {
	for _, tc := range []struct {
		name, alias, body string
		unsupported       bool
	}{
		{"alias import", "operation", `operation.Rule("规则")`, false},
		{"dot import", ".", `Rule("规则")`, true},
		{"function alias", "ops", `reject := ops.Rule; reject("规则")`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "source.go", "package fixture\nimport "+tc.alias+` "github.com/wzhlinhai/salon/backend/internal/ops"`+"\nfunc reject() {"+tc.body+"}", parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			missing, err := missingRuleMessages(fset, file, nil)
			if tc.unsupported {
				if err == nil || !strings.Contains(err.Error(), "unsupported ops.Rule form") {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil || len(missing) != 1 || missing[0].text != "规则" {
				t.Fatalf("missing=%v err=%v", missing, err)
			}
		})
	}
}
