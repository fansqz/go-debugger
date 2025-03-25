package c_debugger

import (
	"github.com/google/go-dap"
	"io"
	"os"
	"os/exec"
	"path"
	"testing"

	"github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/utils"
	"github.com/stretchr/testify/assert"
)

func TestDebug(t *testing.T) {
	var cha = make(chan dap.EventMessage, 10)

	workPath := path.Join("/var/fanCode/tempDir", utils.GetUUID())
	defer os.RemoveAll(workPath)

	execFile, err := compileFile(workPath, "debug.c")
	assert.Nil(t, err)
	debug := NewCDebugger()
	err = debug.Start(&debugger.StartOption{
		ExecFile: execFile,
		// Callback 事件回调
		Callback: func(message dap.EventMessage) {
			cha <- message
		},
	})
	assert.Nil(t, err)

	// 设置断点
	err = debug.SetBreakpoints(dap.Source{Path: "main.c"}, []dap.SourceBreakpoint{
		{Line: 3},
		{Line: 7},
	})
	assert.Nil(t, err)

	// 接受调试编译成功信息
	err = debug.Run()
	assert.Nil(t, err)
	event := <-cha
	assert.Equal(t, "continued", event.GetEvent().Event)
	event = <-cha
	assert.Equal(t, "stopped", event.GetEvent().Event)
	assert.Equal(t, getStoppedLine(debug), 3)

	// 执行next
	err = debug.StepOver()
	assert.Nil(t, err)
	event = <-cha
	assert.Equal(t, "continued", event.GetEvent().Event)
	event = <-cha
	assert.Equal(t, "stopped", event.GetEvent().Event)
	assert.Equal(t, getStoppedLine(debug), 5)

	// 执行continue
	err = debug.Continue()
	assert.Nil(t, err)
	event = <-cha
	assert.Equal(t, "continued", event.GetEvent().Event)

	// 模拟用户输入
	r, w, _ := os.Pipe()
	os.Stdin = r
	io.WriteString(w, "10\n")
	event = <-cha
	assert.Equal(t, "stopped", event.GetEvent().Event)

	// 执行结束
	err = debug.Continue()
	event = <-cha
	assert.Equal(t, "continued", event.GetEvent().Event)
	event = <-cha
	assert.Equal(t, "terminated", event.GetEvent().Event)
}

func TestVariable(t *testing.T) {

	workPath := path.Join("/var/fanCode/tempDir", utils.GetUUID())
	defer os.RemoveAll(workPath)

	// 编译
	execFile, err := compileFile(workPath, "variable.c")
	assert.Nil(t, err)

	// 创建debugger
	debug := NewCDebugger()
	var cha = make(chan dap.EventMessage, 10)
	err = debug.Start(&debugger.StartOption{
		ExecFile: execFile,
		// Callback 事件回调
		Callback: func(message dap.EventMessage) {
			cha <- message
		},
	})
	assert.Nil(t, err)

	// 设置断点
	err = debug.SetBreakpoints(dap.Source{Path: "main.c"}, []dap.SourceBreakpoint{
		{Line: 55},
		{Line: 76},
	})
	assert.Nil(t, err)

	// 启动调试
	err = debug.Run()
	assert.Nil(t, err)
	event := <-cha
	assert.Equal(t, "continued", event.GetEvent().Event)
	event = <-cha
	assert.Equal(t, "stopped", event.GetEvent().Event)

	// 测试第一个断点中的变量信息
	stacks, err := debug.GetStackTrace()
	assert.Nil(t, err)

	// 校验作用域
	scopes, err := debug.GetScopes(stacks[0].Id)
	assert.Equal(t, []dap.Scope{
		{Name: "Global", VariablesReference: 1001},
		{Name: "Local", VariablesReference: 1002},
	}, scopes)

	// 全局变量检查
	globalVariables, err := debug.GetVariables(scopes[0].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "globalChar", Value: "65 'A'", Type: "char"},
		{Name: "globalFloat", Value: "3.1400001", Type: "float"},
		{Name: "globalInt", Value: "10", Type: "int"},
		{Name: "globalItem", Value: "", Type: "Item", VariablesReference: 1100},
	}, globalVariables[0:4])
	assert.Equal(t, []dap.Variable{{Name: "staticGlobalInt", Value: "20", Type: "int"}}, globalVariables[5:6])
	assert.Equal(t, "globalItemPtr", globalVariables[4].Name)
	assert.Equal(t, "Item *", globalVariables[4].Type)
	assert.Equal(t, 1101, globalVariables[4].VariablesReference)
	globalItem, err := debug.GetVariables(globalVariables[4].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "id", Value: "1", Type: "int"},
		{Name: "weight", Value: "65.5", Type: "float"},
		{Name: "color", Value: "RED", Type: "Color"},
	}, globalItem)
	globalItemPtr, err := debug.GetVariables(globalVariables[4].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "id", Value: "1", Type: "int"},
		{Name: "weight", Value: "65.5", Type: "float"},
		{Name: "color", Value: "RED", Type: "Color"},
	}, globalItemPtr)

	localVariables, err := debug.GetVariables(scopes[1].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "argint", Value: "2", Type: "int"},
		{Name: "localInt", Value: "5", Type: "int"},
		{Name: "localChar", Value: "71 'G'", Type: "char"},
		{Name: "staticLocalFloat", Value: "6.78000021", Type: "float"},
		{Name: "localItem", Value: "", Type: "Item", VariablesReference: 1102},
		{Name: "localColor", Value: "BLUE", Type: "Color"},
		{Name: "localValue", Value: "", Type: "Value", VariablesReference: 1103},
	}, localVariables)
	localItem, err := debug.getVariables(localVariables[4].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "id", Value: "2", Type: "int"},
		{Name: "weight", Value: "42", Type: "float"},
		{Name: "color", Value: "GREEN", Type: "Color"},
	}, localItem)
	localValue, err := debug.getVariables(localVariables[6].VariablesReference)
	assert.Nil(t, err)
	assert.Equal(t, []dap.Variable{
		{Name: "ival", Value: "123", Type: "int"},
		{Name: "fval", Value: "1.72359711e-43", Type: "float"},
		{Name: "cval", Value: "123 '{'", Type: "char"},
	}, localValue)

	err = debug.Continue()
	assert.Nil(t, err)
	event = <-cha
	assert.Equal(t, "continued", event.GetEvent().Event)
	event = <-cha
	assert.Equal(t, "stopped", event.GetEvent().Event)
}

func getStoppedLine(gdb debugger.Debugger) int {
	stackTrace, _ := gdb.GetStackTrace()
	if len(stackTrace) != 0 {
		return stackTrace[0].Line
	}
	return 0
}

// compileFile 开始编译文件
func compileFile(workPath string, cFile string) (string, error) {
	// 创建工作目录, 用户的临时文件
	if err := os.MkdirAll(workPath, os.ModePerm); err != nil {
		return "", err
	}

	// 保存待编译文件
	codeFile := path.Join(workPath, "main.c")
	code, err := os.ReadFile(path.Join("./test_file", cFile))
	if err != nil {
		return "", err
	}
	err = os.WriteFile(codeFile, code, 777)
	if err != nil {
		return "", err
	}
	execFile := path.Join(workPath, "main")

	cmd := exec.Command("gcc", "-g", "-o", execFile, codeFile)
	_, err = cmd.Output()
	if err != nil {
		return "", err
	}
	return execFile, err
}
