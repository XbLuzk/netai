package indexer

import (
	"errors"
	"path/filepath"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	treeSitterGo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	treeSitterPython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	treeSitterTypeScript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Language 封装 tree-sitter language 和对应的 queries。
type Language struct {
	TSLanguage    *sitter.Language
	FuncDefQuery  string
	FuncCallQuery string
}

var ErrUnsupportedLanguage = errors.New("unsupported language")

const goFuncDefQuery = `
(function_declaration name: (identifier) @func.name) @func.def
(method_declaration name: (field_identifier) @func.name) @func.def
`

const goFuncCallQuery = `
(call_expression function: [
  (identifier) @call.target
  (selector_expression field: (field_identifier) @call.target)
])
`

const pythonFuncDefQuery = `
(function_definition name: (identifier) @func.name) @func.def
`

const pythonFuncCallQuery = `
(call function: [
  (identifier) @call.target
  (attribute attribute: (identifier) @call.target)
])
`

const typeScriptFuncDefQuery = `
(function_declaration name: (identifier) @func.name) @func.def
(method_definition name: (property_identifier) @func.name) @func.def
(arrow_function) @func.def
`

const typeScriptFuncCallQuery = `
(call_expression function: [
  (identifier) @call.target
  (member_expression property: (property_identifier) @call.target)
])
`

// DetectLanguage 根据文件扩展名返回 Language。
// 支持：.go → Go，.py → Python，.ts/.tsx → TypeScript。
// 不支持的扩展名返回 ErrUnsupportedLanguage。
func DetectLanguage(filename string) (*Language, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	switch ext {
	case ".go":
		return &Language{
			TSLanguage:    sitter.NewLanguage(treeSitterGo.Language()),
			FuncDefQuery:  goFuncDefQuery,
			FuncCallQuery: goFuncCallQuery,
		}, nil
	case ".py":
		return &Language{
			TSLanguage:    sitter.NewLanguage(treeSitterPython.Language()),
			FuncDefQuery:  pythonFuncDefQuery,
			FuncCallQuery: pythonFuncCallQuery,
		}, nil
	case ".ts":
		return &Language{
			TSLanguage:    sitter.NewLanguage(treeSitterTypeScript.LanguageTypescript()),
			FuncDefQuery:  typeScriptFuncDefQuery,
			FuncCallQuery: typeScriptFuncCallQuery,
		}, nil
	case ".tsx":
		return &Language{
			TSLanguage:    sitter.NewLanguage(treeSitterTypeScript.LanguageTSX()),
			FuncDefQuery:  typeScriptFuncDefQuery,
			FuncCallQuery: typeScriptFuncCallQuery,
		}, nil
	default:
		return nil, ErrUnsupportedLanguage
	}
}
