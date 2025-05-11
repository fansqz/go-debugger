package cpp_debugger

import (
	"fmt"
	"github.com/google/go-dap"
	"io"
	"os"
	"path"
	"testing"

	"github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/utils"
	"github.com/stretchr/testify/assert"
)

// testHelper 测试辅助结构体,封装测试所需的通用组件
type testHelper struct {
	t        *testing.T
	workPath string
	debug    *CPPDebugger
	eventCh  chan dap.EventMessage
}

// newTestHelper 创建新的测试辅助实例
func newTestHelper(t *testing.T) *testHelper {
	workPath := path.Join("/var/fanCode/tempDir", utils.GetUUID())
	debug := NewCPPDebugger()
	eventCh := make(chan dap.EventMessage, 10)

	return &testHelper{
		t:        t,
		workPath: workPath,
		debug:    debug,
		eventCh:  eventCh,
	}
}

// setup 设置测试环境
func (h *testHelper) setup(cppFile string) {
	// 编译测试文件
	execFile, code, err := compileFile(h.workPath, cppFile)
	assert.Nil(h.t, err)

	// 启动调试器
	err = h.debug.Start(&debugger.StartOption{
		MainCode: code,
		ExecFile: execFile,
		Callback: func(message dap.EventMessage) {
			h.eventCh <- message
		},
	})
	assert.Nil(h.t, err)
}

// cleanup 清理测试环境
func (h *testHelper) cleanup() {
	os.RemoveAll(h.workPath)
}

// waitForEvent 等待并验证事件
func (h *testHelper) waitForEvent(expectedEvent string) dap.EventMessage {
	event := <-h.eventCh
	assert.Equal(h.t, expectedEvent, event.GetEvent().Event)
	return event
}

// TestDebug 测试普通调试功能
func TestDebug(t *testing.T) {
	helper := newTestHelper(t)
	defer helper.cleanup()

	helper.setup("debug.cpp")

	// 设置断点
	err := helper.debug.SetBreakpoints(dap.Source{Path: "main.cpp"}, []dap.SourceBreakpoint{
		{Line: 3},
		{Line: 7},
	})
	assert.Nil(t, err)

	// 启动调试并验证初始事件
	err = helper.debug.Run()
	assert.Nil(t, err)
	helper.waitForEvent("continued")
	helper.waitForEvent("stopped")
	assert.Equal(t, 3, getStoppedLine(helper.debug))

	// 测试单步执行
	err = helper.debug.StepOver()
	assert.Nil(t, err)
	helper.waitForEvent("continued")
	helper.waitForEvent("stopped")
	assert.Equal(t, 4, getStoppedLine(helper.debug))

	// 测试继续执行
	err = helper.debug.Continue()
	assert.Nil(t, err)
	helper.waitForEvent("continued")

	// 模拟用户输入
	r, w, _ := os.Pipe()
	os.Stdin = r
	io.WriteString(w, "10\n")
	helper.waitForEvent("stopped")

	// 验证程序结束
	err = helper.debug.Continue()
	helper.waitForEvent("continued")
	helper.waitForEvent("terminated")
}

// TestVariable 测试变量获取
func TestVariable(t *testing.T) {
	helper := newTestHelper(t)
	defer helper.cleanup()

	helper.setup("variable.cpp")

	// 设置断点
	err := helper.debug.SetBreakpoints(dap.Source{Path: "main.cpp"}, []dap.SourceBreakpoint{
		{Line: 64},
		{Line: 81},
	})
	assert.Nil(t, err)

	// 启动调试
	err = helper.debug.Run()
	assert.Nil(t, err)
	helper.waitForEvent("continued")
	helper.waitForEvent("stopped")

	// 获取并验证栈帧信息
	stacks, err := helper.debug.GetStackTrace()
	assert.Nil(t, err)

	// 验证作用域
	scopes, err := helper.debug.GetScopes(stacks[0].Id)
	assert.Equal(t, []dap.Scope{
		{Name: "Global", VariablesReference: 1001},
		{Name: "Local", VariablesReference: 1002},
	}, scopes)

	// 验证全局变量
	verifyGlobalVariables(t, helper.debug, scopes[0].VariablesReference)

	// 验证局部变量
	verifyLocalVariables(t, helper.debug, scopes[1].VariablesReference)

	// 继续执行到下一个断点
	err = helper.debug.Continue()
	assert.Nil(t, err)
	helper.waitForEvent("continued")
	helper.waitForEvent("stopped")

	scopes, err = helper.debug.GetScopes(stacks[0].Id)
	assert.Equal(t, []dap.Scope{
		{Name: "Global", VariablesReference: 1001},
		{Name: "Local", VariablesReference: 1002},
	}, scopes)
	verifyLocalVariables2(t, helper.debug, scopes[1].VariablesReference)
}

// verifyGlobalVariables 验证全局变量
func verifyGlobalVariables(t *testing.T, debug *CPPDebugger, ref int) {
	variables, err := debug.GetVariables(ref)
	assert.Nil(t, err)

	// 验证基本类型变量
	assert.Equal(t, []dap.Variable{
		{Name: "globalChar", Value: "65 'A'", Type: "char"},
		{Name: "globalFloat", Value: "3.1400001", Type: "float"},
		{Name: "globalInt", Value: "10", Type: "int"},
		{Name: "globalItem", Value: "", Type: "Item", VariablesReference: 1100, IndexedVariables: 1},
	}, variables[0:4])

	// 验证静态全局变量
	assert.Equal(t, []dap.Variable{{Name: "staticGlobalInt", Value: "20", Type: "int"}}, variables[5:6])

	// 验证智能指针变量
	assert.Equal(t, "globalItemPtr", variables[4].Name)
	assert.Equal(t, "std::unique_ptr<Item, std::default_delete<Item> >", variables[4].Type)
	assert.Equal(t, 1101, variables[4].VariablesReference)

	// 验证结构体成员
	globalItem, err := debug.GetVariables(variables[3].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "id", Value: "1", Type: "int"},
		{Name: "weight", Value: "65.5", Type: "float"},
		{Name: "color", Value: "Color::RED", Type: "Color"},
	}, globalItem)
}

// verifyLocalVariables 验证局部变量
func verifyLocalVariables(t *testing.T, debug *CPPDebugger, ref int) {
	variables, err := debug.GetVariables(ref)
	assert.Nil(t, err)

	assert.Equal(t, []dap.Variable{
		{Name: "argint", Value: "2", Type: "int"},
		{Name: "localInt", Value: "5", Type: "int"},
		{Name: "localChar", Value: "71 'G'", Type: "char"},
		{Name: "staticLocalFloat", Value: "6.78000021", Type: "float"},
		{Name: "localItem", Value: "", Type: "Item", VariablesReference: 1102},
		{Name: "localColor", Value: "Color::RED", Type: "Color"},
		{Name: "localValue", Value: "", Type: "Value", VariablesReference: 1103},
	}, variables)

	// 验证局部结构体成员
	localItem, err := debug.GetVariables(variables[4].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "id", Value: "2", Type: "int"},
		{Name: "weight", Value: "42", Type: "float"},
		{Name: "color", Value: "GREEN", Type: "Color"},
	}, localItem)

	// 验证联合体成员
	localValue, err := debug.GetVariables(variables[5].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "ival", Value: "123", Type: "int"},
		{Name: "fval", Value: "1.72359711e-43", Type: "float"},
		{Name: "cval", Value: "123 '{'", Type: "char"},
	}, localValue)
}

// verifyLocalVariables2 验证局部变量
func verifyLocalVariables2(t *testing.T, debug *CPPDebugger, ref int) {
	variables, err := debug.GetVariables(ref)
	assert.Nil(t, err)
	assert.Equal(t, 5, len(variables))
	arrayVars, err := debug.GetVariables(variables[4].VariablesReference)
	assert.Nil(t, err)
	fmt.Println(arrayVars)
}

// verifyLocalPointVariables
func verifyLocalPointVariables(t *testing.T, debug *CPPDebugger, ref int) {
	variables, err := debug.GetVariables(ref)
	assert.Nil(t, err)
	fmt.Println(variables)
}

// getStoppedLine 获取当前停止的行号
func getStoppedLine(gdb debugger.Debugger) int {
	stackTrace, _ := gdb.GetStackTrace()
	if len(stackTrace) != 0 {
		return stackTrace[0].Line
	}
	return 0
}

// compileFile 编译C++源文件
func compileFile(workPath string, cppFile string) (string, string, error) {
	// 创建工作目录
	if err := os.MkdirAll(workPath, os.ModePerm); err != nil {
		return "", "", err
	}

	// 保存源文件
	code, err := os.ReadFile(path.Join("./test_file", cppFile))
	if err != nil {
		return "", "", err
	}
	// 编译文件
	execFile, err := CompileCPPFile(workPath, string(code))
	return execFile, string(code), err
}
