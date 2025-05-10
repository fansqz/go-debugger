package utils

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
)

// VariableInfo 存储变量信息
type VariableInfo struct {
	Name     string
	Type     string
	Location struct {
		Line   int
		Column int
	}
}

// FunctionInfo 存储函数信息
type FunctionInfo struct {
	Name     string
	Location struct {
		Line   int
		Column int
	}
}

// ParseCFile 解析C语言文件并返回变量和函数信息
func ParseCFile(sourceCode string) ([]VariableInfo, []FunctionInfo, error) {
	// 创建解析器
	parser := sitter.NewParser()
	parser.SetLanguage(c.GetLanguage())

	// 解析源代码
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(sourceCode))
	if err != nil {
		return nil, nil, fmt.Errorf("解析失败: %v", err)
	}
	defer tree.Close()

	var variables []VariableInfo
	var functions []FunctionInfo

	// 遍历语法树
	cursor := sitter.NewTreeCursor(tree.RootNode())
	defer cursor.Close()

	// 递归遍历节点
	var traverse func(*sitter.TreeCursor)
	traverse = func(cursor *sitter.TreeCursor) {
		node := cursor.CurrentNode()

		// 检查变量声明
		if node.Type() == "declaration" {
			// 获取变量名
			declarator := node.ChildByFieldName("declarator")
			if declarator != nil {
				identifier := declarator.ChildByFieldName("declarator")
				if identifier != nil {
					varInfo := VariableInfo{
						Name: identifier.Content([]byte(sourceCode)),
						Type: node.ChildByFieldName("type").Content([]byte(sourceCode)),
					}
					varInfo.Location.Line = int(identifier.StartPoint().Row + 1)
					varInfo.Location.Column = int(identifier.StartPoint().Column + 1)
					variables = append(variables, varInfo)
				}
			}
		}

		// 检查函数定义
		if node.Type() == "function_definition" {
			declarator := node.ChildByFieldName("declarator")
			if declarator != nil {
				identifier := declarator.ChildByFieldName("declarator")
				if identifier != nil {
					funcInfo := FunctionInfo{
						Name: identifier.Content([]byte(sourceCode)),
					}
					funcInfo.Location.Line = int(identifier.StartPoint().Row + 1)
					funcInfo.Location.Column = int(identifier.StartPoint().Column + 1)
					functions = append(functions, funcInfo)
				}
			}
		}

		// 递归遍历子节点
		if cursor.GoToFirstChild() {
			traverse(cursor)
			for cursor.GoToNextSibling() {
				traverse(cursor)
			}
			cursor.GoToParent()
		}
	}

	traverse(cursor)
	return variables, functions, nil
}
