package testpolicy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// calendarCalls 找出未先转换门店时区就对 time.Now() 取日历值的调用。
func calendarCalls(file *ast.File) []token.Pos {
	aliases := map[string]bool{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "time" {
			continue
		}
		name := "time"
		if imp.Name != nil {
			name = imp.Name.Name
		}
		aliases[name] = true
	}
	var positions []token.Pos
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch method.Sel.Name {
		case "AddDate", "Date", "Year", "Month", "Day", "Weekday", "YearDay", "Hour", "Clock":
		default:
			return true
		}
		now, ok := ast.Unparen(method.X).(*ast.CallExpr)
		if !ok || len(now.Args) != 0 {
			return true
		}
		switch fun := ast.Unparen(now.Fun).(type) {
		case *ast.SelectorExpr:
			pkg, ok := fun.X.(*ast.Ident)
			if ok && pkg.Obj == nil && aliases[pkg.Name] && fun.Sel.Name == "Now" {
				positions = append(positions, call.Pos())
			}
		case *ast.Ident:
			if aliases["."] && fun.Obj == nil && fun.Name == "Now" {
				positions = append(positions, call.Pos())
			}
		}
		return true
	})
	return positions
}

func TestCalendarUsesBusinessDate(t *testing.T) {
	// 独立测试包不依赖数据库，CI 的 go test ./... 会遍历整个 backend。
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		for _, pos := range calendarCalls(file) {
			t.Errorf("%s: compute from the store's business date: time.Now().In(settings.Location) or settings.Date", fset.Position(pos))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCalendarCalls(t *testing.T) {
	t.Run("absolute time truncation", func(t *testing.T) {
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", `package fixture
import "time"
func test() { time.Now().Truncate(time.Second) }`, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(calendarCalls(file)); got != 0 {
			t.Fatalf("found %d calls, want 0", got)
		}
	})
	for _, method := range []string{"AddDate(0, 0, -1)", "Date()", "Year()", "Month()", "Day()", "Weekday()", "YearDay()", "Hour()", "Clock()"} {
		for _, tc := range []struct {
			name, imports, expr string
			want                int
		}{
			{"direct", `import "time"`, "time.Now()." + method, 1},
			{"alias", `import clock "time"`, "clock.Now()." + method, 1},
			{"dot", `import . "time"`, "Now()." + method, 1},
			{"parentheses", `import "time"`, "(time.Now())." + method, 1},
			{"store zone", `import "time"`, "time.Now().In(settings.Location)." + method, 0},
			{"business date", `import "time"`, "settings.Date(time.Now())." + method, 0},
			{"other package", `import "other"`, "other.Now()." + method, 0},
			{"shadowed", `import "time"`, "func() { time := clock; time.Now()." + method + " }()", 0},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package fixture\n"+tc.imports+"\nfunc test() { "+tc.expr+" }", 0)
				if err != nil {
					t.Fatal(err)
				}
				if got := len(calendarCalls(file)); got != tc.want {
					t.Fatalf("found %d calls, want %d", got, tc.want)
				}
			})
		}
	}
}
