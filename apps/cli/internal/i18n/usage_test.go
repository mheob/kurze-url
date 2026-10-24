package i18n

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

	"github.com/stretchr/testify/require"
)

// moduleRoot is the CLI module's root as seen from this package's directory.
const moduleRoot = "../.."

// usageProblems reports every call of a method named T in file whose key the
// catalogs cannot be checked against or do not define, one "file:line: …"
// line each.
//
// The rule is syntactic on purpose. A key held in a variable, a constant or a
// concatenation could name something the catalog lacks and no test would
// ever find out, so the first argument has to be a string literal that the
// tests can look up. Code that picks a key from a value (a code, a role, a
// state) does it with a switch whose every case calls T with its own
// literal. Only a call written as x.T(…) is seen: a method value such as
// f := c.T passes its keys where this scan cannot follow them.
func usageProblems(fset *token.FileSet, file *ast.File, defined map[string]string) []string {
	var problems []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "T" || len(call.Args) == 0 {
			return true
		}

		pos := fset.Position(call.Args[0].Pos())
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: the key passed to T must be a string literal", pos.Filename, pos.Line))
			return true
		}
		key, err := strconv.Unquote(lit.Value)
		if err != nil {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: unreadable key literal %s: %v", pos.Filename, pos.Line, lit.Value, err))
			return true
		}
		if _, ok := defined[key]; !ok {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: key %q is not defined in en.json", pos.Filename, pos.Line, key))
		}
		return true
	})
	return problems
}

func TestEveryUsedKeyIsDefined(t *testing.T) {
	defined := mustMessages(t, EN)
	// A wrong root would parse nothing and pass, so insist that it is the
	// module and that files were actually read.
	require.FileExists(t, filepath.Join(moduleRoot, "go.mod"))

	fset := token.NewFileSet()
	var problems []string
	parsed := 0
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		// Tests call T with throwaway keys and fixtures of their own.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		parsed++
		problems = append(problems, usageProblems(fset, file, defined)...)
		return nil
	})
	require.NoError(t, err)
	require.NotZero(t, parsed, "no Go files found under %s", moduleRoot)

	require.Empty(t, problems, "keys passed to T:\n%s", strings.Join(problems, "\n"))
}

// With no calls to T in the tree yet, the test above passes whatever
// usageProblems does. This pins what it reports.
func TestUsageProblemsReportsWhatItShould(t *testing.T) {
	src := strings.Join([]string{
		"package probe",               // 1
		"",                            // 2
		"func f(c cat, key string) {", // 3
		`	c.T("known")`,               // 4: defined
		`	c.T("missing.key")`,         // 5: not defined
		"	c.T(key)",                   // 6: not a literal
		`	c.T("kn" + "own")`,          // 7: not a literal, although it adds up
		"	c.T()",                      // 8: no key, not a lookup
		"	c.T(`known`)",               // 9: a raw string is a literal
		`	c.Other("never.looked.up")`, // 10: another method
		"}",                           // 11
	}, "\n")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)

	problems := usageProblems(fset, file, map[string]string{"known": "Known"})

	require.Len(t, problems, 3, strings.Join(problems, "\n"))
	require.Contains(t, problems[0], "probe.go:5:")
	require.Contains(t, problems[0], `"missing.key" is not defined`)
	require.Contains(t, problems[1], "probe.go:6:")
	require.Contains(t, problems[1], "must be a string literal")
	require.Contains(t, problems[2], "probe.go:7:")
	require.Contains(t, problems[2], "must be a string literal")
}
