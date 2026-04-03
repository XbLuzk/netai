package indexer

import (
	"fmt"
	"os"
)

import sitter "github.com/tree-sitter/go-tree-sitter"

// Function 表示从源文件解析出的函数。
type Function struct {
	Name      string
	FilePath  string
	StartLine int
	EndLine   int
	Source    string
}

// RawCallEdge 表示原始调用关系（callee 还未解析为 UUID）。
type RawCallEdge struct {
	CallerName string
	CalleeName string
}

// ParseFile 解析单个源文件，返回函数列表和原始调用关系。
// 返回的 RawCallEdge.CallerName 是调用发生所在的函数名。
// 文件读取失败 → 返回 error。
// 语言不支持 → 返回 ErrUnsupportedLanguage（调用方可跳过）。
// Tree-sitter 语法错误 → 容错解析，返回已识别部分（不 panic）。
func ParseFile(filePath string) ([]Function, []RawCallEdge, error) {
	lang, err := DetectLanguage(filePath)
	if err != nil {
		return nil, nil, err
	}

	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read file %q: %w", filePath, err)
	}

	functions := make([]Function, 0)
	edges := make([]RawCallEdge, 0)

	if len(src) == 0 {
		return functions, edges, nil
	}

	parser := sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(lang.TSLanguage); err != nil {
		return nil, nil, fmt.Errorf("set language for %q: %w", filePath, err)
	}

	tree := parser.Parse(src, nil)
	if tree == nil {
		return functions, edges, nil
	}
	defer tree.Close()

	root := tree.RootNode()

	funcDefQuery, qErr := sitter.NewQuery(lang.TSLanguage, lang.FuncDefQuery)
	if qErr != nil {
		return nil, nil, qErr
	}
	defer funcDefQuery.Close()

	funcDefCursor := sitter.NewQueryCursor()
	defer funcDefCursor.Close()

	funcDefMatches := funcDefCursor.Matches(funcDefQuery, root, src)
	funcDefCaptureNames := funcDefQuery.CaptureNames()

	for {
		match := funcDefMatches.Next()
		if match == nil {
			break
		}

		var funcDefNode *sitter.Node
		var funcNameNode *sitter.Node

		for _, capture := range match.Captures {
			captureName := funcDefCaptureNames[capture.Index]
			capturedNode := capture.Node

			switch captureName {
			case "func.def":
				n := capturedNode
				funcDefNode = &n
			case "func.name":
				n := capturedNode
				funcNameNode = &n
			}
		}

		if funcDefNode == nil {
			continue
		}

		funcName := ""
		if funcNameNode != nil {
			funcName = safeNodeText(src, funcNameNode)
		}

		// TypeScript arrow_function 没有明确名称，保留稳定占位名。
		if funcName == "" {
			start := funcDefNode.StartPosition()
			funcName = fmt.Sprintf("<anonymous@%d>", int(start.Row)+1)
		}

		startPos := funcDefNode.StartPosition()
		endPos := funcDefNode.EndPosition()

		functions = append(functions, Function{
			Name:      funcName,
			FilePath:  filePath,
			StartLine: int(startPos.Row) + 1,
			EndLine:   int(endPos.Row) + 1,
			Source:    safeNodeText(src, funcDefNode),
		})
	}

	funcCallQuery, qErr := sitter.NewQuery(lang.TSLanguage, lang.FuncCallQuery)
	if qErr != nil {
		return nil, nil, qErr
	}
	defer funcCallQuery.Close()

	funcCallCursor := sitter.NewQueryCursor()
	defer funcCallCursor.Close()

	funcCallMatches := funcCallCursor.Matches(funcCallQuery, root, src)
	funcCallCaptureNames := funcCallQuery.CaptureNames()

	for {
		match := funcCallMatches.Next()
		if match == nil {
			break
		}

		var calleeName string
		var callLine int

		for _, capture := range match.Captures {
			if funcCallCaptureNames[capture.Index] != "call.target" {
				continue
			}

			n := capture.Node
			calleeName = safeNodeText(src, &n)
			callLine = int(n.StartPosition().Row) + 1
			break
		}

		if calleeName == "" || callLine == 0 {
			continue
		}

		caller := findEnclosingFunction(functions, callLine)
		if caller == nil {
			continue
		}

		edges = append(edges, RawCallEdge{
			CallerName: caller.Name,
			CalleeName: calleeName,
		})
	}

	return functions, edges, nil
}

func safeNodeText(src []byte, node *sitter.Node) string {
	if node == nil {
		return ""
	}

	start := int(node.StartByte())
	end := int(node.EndByte())
	if start < 0 || end < 0 || start > end || end > len(src) {
		return ""
	}

	return string(src[start:end])
}

func findEnclosingFunction(functions []Function, line int) *Function {
	var best *Function
	bestRange := int(^uint(0) >> 1)

	for i := range functions {
		fn := &functions[i]
		if line < fn.StartLine || line > fn.EndLine {
			continue
		}

		r := fn.EndLine - fn.StartLine
		if best == nil || r < bestRange {
			best = fn
			bestRange = r
		}
	}

	return best
}
