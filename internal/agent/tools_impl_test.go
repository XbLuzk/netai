package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeQuerier struct {
	queryRowFn func(ctx context.Context, sql string, args ...any) pgx.Row
	queryFn    func(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (f *fakeQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return f.queryRowFn(ctx, sql, args...)
}

func (f *fakeQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return f.queryFn(ctx, sql, args...)
}

type fakeRow struct {
	values []any
	err    error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("scan destination mismatch")
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = r.values[i].(string)
		default:
			return errors.New("unsupported destination type")
		}
	}
	return nil
}

type fakeRows struct {
	rows [][]any
	idx  int
	err  error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func (r *fakeRows) Next() bool {
	return r.idx < len(r.rows)
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.idx >= len(r.rows) {
		return errors.New("out of rows")
	}
	row := r.rows[r.idx]
	r.idx++
	if len(dest) != len(row) {
		return errors.New("scan destination mismatch")
	}

	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = row[i].(string)
		case *bool:
			*d = row[i].(bool)
		case *float32:
			*d = row[i].(float32)
		default:
			return errors.New("unsupported destination type")
		}
	}
	return nil
}

func TestGetFunctionSource(t *testing.T) {
	var gotSQL string
	var gotArgs []any

	impl := &ToolsImpl{
		pool: &fakeQuerier{
			queryRowFn: func(ctx context.Context, sql string, args ...any) pgx.Row {
				gotSQL = sql
				gotArgs = args
				return &fakeRow{values: []any{"func Foo() {}"}}
			},
		},
	}

	source, err := impl.GetFunctionSource(context.Background(), "repo-1", "Foo")
	if err != nil {
		t.Fatalf("GetFunctionSource error: %v", err)
	}
	if source != "func Foo() {}" {
		t.Fatalf("unexpected source: %q", source)
	}
	if !strings.Contains(gotSQL, "SELECT source") {
		t.Fatalf("unexpected sql: %q", gotSQL)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "repo-1" || gotArgs[1] != "Foo" {
		t.Fatalf("unexpected args: %#v", gotArgs)
	}
}

func TestGetCallees(t *testing.T) {
	impl := &ToolsImpl{
		pool: &fakeQuerier{
			queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "UNION ALL") {
					t.Fatalf("expected UNION ALL in sql, got %q", sql)
				}
				if len(args) != 2 || args[0] != "repo-1" || args[1] != "Handler" {
					t.Fatalf("unexpected args: %#v", args)
				}
				return &fakeRows{rows: [][]any{
					{"HandleRequest", "handler.go", false},
					{"net/http.(*Client).Do", "", true},
				}}, nil
			},
		},
	}

	callees, err := impl.GetCallees(context.Background(), "repo-1", "Handler")
	if err != nil {
		t.Fatalf("GetCallees error: %v", err)
	}
	if len(callees) != 2 {
		t.Fatalf("expected 2 callees, got %d", len(callees))
	}
	if callees[0].External {
		t.Fatalf("expected first callee to be resolved")
	}
	if !callees[1].External {
		t.Fatalf("expected second callee to be unresolved external")
	}
}

func TestGetCallers(t *testing.T) {
	impl := &ToolsImpl{
		pool: &fakeQuerier{
			queryFn: func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "JOIN functions callee") {
					t.Fatalf("unexpected sql: %q", sql)
				}
				if len(args) != 2 || args[0] != "repo-1" || args[1] != "ServeHTTP" {
					t.Fatalf("unexpected args: %#v", args)
				}
				return &fakeRows{rows: [][]any{
					{"main", "main.go"},
					{"bootstrap", "bootstrap.go"},
				}}, nil
			},
		},
	}

	callers, err := impl.GetCallers(context.Background(), "repo-1", "ServeHTTP")
	if err != nil {
		t.Fatalf("GetCallers error: %v", err)
	}
	if len(callers) != 2 {
		t.Fatalf("expected 2 callers, got %d", len(callers))
	}
	if callers[0].Name != "main" || callers[0].FilePath != "main.go" {
		t.Fatalf("unexpected first caller: %+v", callers[0])
	}
}
