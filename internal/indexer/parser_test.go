package indexer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const goFixture = `
package main

import "fmt"

func hello() {
    fmt.Println("hello")
    greet("world")
}

func greet(name string) {
    fmt.Printf("hello %s\\n", name)
}
`

const pythonFixture = `
def hello():
    print("hello")
    greet("world")

def greet(name):
    print(name)
`

func TestParseGoFile(t *testing.T) {
	path := writeFixture(t, "main.go", goFixture)

	functions, edges, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if len(functions) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(functions))
	}

	requireFunction(t, functions, "hello")
	requireFunction(t, functions, "greet")

	callsByCaller := edgesByCaller(edges)

	requireCallee(t, callsByCaller, "hello", "Println")
	requireCallee(t, callsByCaller, "hello", "greet")
	requireCallee(t, callsByCaller, "greet", "Printf")
}

func TestParseEmptyFile(t *testing.T) {
	path := writeFixture(t, "empty.go", "")

	functions, edges, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if len(functions) != 0 {
		t.Fatalf("expected 0 functions, got %d", len(functions))
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(edges))
	}
}

func TestUnsupportedLanguage(t *testing.T) {
	path := writeFixture(t, "main.rb", "puts 'hello'\n")

	_, _, err := ParseFile(path)
	if !errors.Is(err, ErrUnsupportedLanguage) {
		t.Fatalf("expected ErrUnsupportedLanguage, got %v", err)
	}
}

func TestParseFileWithSyntaxError(t *testing.T) {
	fixture := `
package main

func ok() {
    hello()
}

func broken(
`
	path := writeFixture(t, "bad.go", fixture)

	functions, edges, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if len(functions) == 0 {
		t.Fatalf("expected partial parse result, got 0 functions")
	}

	callsByCaller := edgesByCaller(edges)
	requireCallee(t, callsByCaller, "ok", "hello")
}

func TestParsePythonFile(t *testing.T) {
	path := writeFixture(t, "main.py", pythonFixture)

	functions, edges, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}

	if len(functions) != 2 {
		t.Fatalf("expected 2 functions, got %d", len(functions))
	}

	requireFunction(t, functions, "hello")
	requireFunction(t, functions, "greet")

	callsByCaller := edgesByCaller(edges)
	requireCallee(t, callsByCaller, "hello", "print")
	requireCallee(t, callsByCaller, "hello", "greet")
	requireCallee(t, callsByCaller, "greet", "print")
}

func writeFixture(t *testing.T, filename string, content string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func requireFunction(t *testing.T, functions []Function, name string) {
	t.Helper()
	for _, fn := range functions {
		if fn.Name == name {
			return
		}
	}
	t.Fatalf("expected function %q not found", name)
}

func edgesByCaller(edges []RawCallEdge) map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{}, len(edges))
	for _, edge := range edges {
		if _, ok := result[edge.CallerName]; !ok {
			result[edge.CallerName] = make(map[string]struct{})
		}
		result[edge.CallerName][edge.CalleeName] = struct{}{}
	}
	return result
}

func requireCallee(t *testing.T, callsByCaller map[string]map[string]struct{}, caller string, callee string) {
	t.Helper()
	calleeSet, ok := callsByCaller[caller]
	if !ok {
		t.Fatalf("expected caller %q not found", caller)
	}
	if _, ok := calleeSet[callee]; !ok {
		t.Fatalf("expected callee %q under caller %q not found", callee, caller)
	}
}
