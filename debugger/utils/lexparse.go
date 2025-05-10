package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fansqz/go-debugger/constants"
	"log"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
)

// VariableInfo 存储变量定义的相关信息
type VariableInfo struct {
	Name         string `json:"name"`
	FunctionName string `json:"functionName"`
	Line         int    `json:"line"`
	Column       int    `json:"column"`
}

func AnalyzeVariables(content []byte, languageType constants.LanguageType) ([]VariableInfo, error) {
	parser := sitter.NewParser()
	switch languageType {
	case constants.LanguageC:
		parser.SetLanguage(c.GetLanguage())
	case constants.LanguageCpp:
		parser.SetLanguage(cpp.GetLanguage())
	}
	parser.SetLanguage(c.GetLanguage())
	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, err
	}
	rootNode := tree.RootNode()
	vars := analyzeVariables(rootNode, content)
	return vars, nil
}

// analyzeVariables 遍历语法树，分析变量定义
func analyzeVariables(rootNode *sitter.Node, content []byte) []VariableInfo {
	var variables []VariableInfo
	var currentFunction string
	var functionStack []string

	// 使用栈来手动管理节点遍历
	stack := []*sitter.Node{rootNode}

	for len(stack) > 0 {
		// 取出栈顶节点
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// 处理函数定义
		if node.Type() == "function_definition" {
			// 获取函数名称
			declarator := node.ChildByFieldName("declarator")
			if declarator != nil {
				functionNameNode := declarator.ChildByFieldName("name")
				if functionNameNode != nil {
					currentFunction = getNodeText(functionNameNode, content)
					functionStack = append(functionStack, currentFunction)
				}
			}
		}

		// 处理变量定义
		if node.Type() == "declaration" {
			// 检查是否是变量声明（排除函数声明等）
			if isVariableDeclaration(node) {
				// 获取变量名和位置信息
				variables = append(variables, extractVariableInfo(node, content, currentFunction)...)
			}
		}

		// 函数结束后重置当前函数名
		if node.Type() == "function_definition" {
			// 检查子节点是否全部处理完毕
			allChildrenProcessed := true
			for i := 0; i < int(node.ChildCount()); i++ {
				child := node.Child(i)
				processed := false
				for _, n := range stack {
					if n == child {
						processed = true
						break
					}
				}
				if !processed {
					allChildrenProcessed = false
					break
				}
			}

			if allChildrenProcessed && len(functionStack) > 0 {
				functionStack = functionStack[:len(functionStack)-1]
				if len(functionStack) > 0 {
					currentFunction = functionStack[len(functionStack)-1]
				} else {
					currentFunction = ""
				}
			}
		}

		// 将子节点压入栈中（从后往前压，确保按正确顺序处理）
		for i := int(node.ChildCount()) - 1; i >= 0; i-- {
			stack = append(stack, node.Child(i))
		}
	}

	return variables
}

// isVariableDeclaration 检查节点是否是变量声明
func isVariableDeclaration(node *sitter.Node) bool {
	// 检查子节点是否包含init_declarator_list
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "init_declarator_list" {
			return true
		}
	}
	return false
}

// extractVariableInfo 从声明节点中提取变量信息
func extractVariableInfo(node *sitter.Node, content []byte, functionName string) []VariableInfo {
	var variables []VariableInfo

	// 查找init_declarator_list节点
	initDeclaratorList := findChildByType(node, "init_declarator_list")
	if initDeclaratorList == nil {
		return variables
	}

	// 遍历每个init_declarator
	for i := 0; i < int(initDeclaratorList.ChildCount()); i++ {
		declarator := initDeclaratorList.Child(i)
		if declarator.Type() == "init_declarator" {
			// 获取变量名节点
			nameNode := declarator.ChildByFieldName("declarator")
			if nameNode != nil {
				// 处理基本变量名
				if nameNode.Type() == "identifier" {
					variables = append(variables, VariableInfo{
						Name:         getNodeText(nameNode, content),
						FunctionName: functionName,
						Line:         int(nameNode.StartPoint().Row + 1),
						Column:       int(nameNode.StartPoint().Column + 1),
					})
				} else {
					// 处理更复杂的声明（如指针、数组等）
					identifier := findChildByType(nameNode, "identifier")
					if identifier != nil {
						variables = append(variables, VariableInfo{
							Name:         getNodeText(identifier, content),
							FunctionName: functionName,
							Line:         int(identifier.StartPoint().Row + 1),
							Column:       int(identifier.StartPoint().Column + 1),
						})
					}
				}
			}
		}
	}

	return variables
}

// findChildByType 在节点的子节点中查找特定类型的节点
func findChildByType(node *sitter.Node, nodeType string) *sitter.Node {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == nodeType {
			return child
		}
	}
	return nil
}

// getNodeText 获取节点对应的源代码文本
func getNodeText(node *sitter.Node, content []byte) string {
	start := node.StartByte()
	end := node.EndByte()
	return string(content[start:end])
}

// outputJSON 将变量信息以JSON格式输出
func outputJSON(variables []VariableInfo) {
	jsonData, err := json.MarshalIndent(variables, "", "  ")
	if err != nil {
		log.Fatalf("生成JSON失败: %v", err)
	}

	// 输出到标准输出
	fmt.Println(string(jsonData))
}
