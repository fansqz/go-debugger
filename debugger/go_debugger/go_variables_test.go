package go_debugger

import (
	"context"
	"github.com/fansqz/go-debugger/constants"
	"github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/utils"
	"github.com/maxatome/go-testdeep/td"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func testGoDebugger_Variable(t *testing.T) {
	var cha = make(chan interface{}, 10)
	executePath := getExecutePath("/var/fanCode/tempDir")
	defer os.RemoveAll(executePath)
	// 保存用户代码到用户的执行路径，并获取编译文件列表
	debug := NewGoDebugger()
	ctx := context.Background()
	err := debug.Start(ctx, &debugger.StartOption{
		WorkPath:     executePath,
		CompileFiles: saveUserCode(t, "./test_file/go_test2", executePath),
		BreakPoints:  []*debugger.Breakpoint{{"/main.go", 33}},
		Callback:     func(data interface{}) { cha <- data },
	})
	assert.Nil(t, err)
	// 程序启动
	assert.Equal(t, debugger.CompileSuccessEvent, <-cha)
	assert.Equal(t, debugger.LaunchSuccessEvent, <-cha)
	assert.Equal(t, &debugger.BreakpointEvent{
		Reason:      constants.NewType,
		Breakpoints: []*debugger.Breakpoint{{"/main.go", 33}},
	}, <-cha)
	// 到达断点，获取栈帧
	assert.Equal(t, &debugger.ContinuedEvent{}, <-cha)
	assert.Equal(t, &debugger.StoppedEvent{Reason: constants.BreakpointStopped, File: "/main.go", Line: 33}, <-cha)
	stackFrames, err := debug.GetStackTrace(ctx)
	assert.Nil(t, err)
	assert.Equal(t, 3, len(stackFrames))
	assert.Equal(t, "0", stackFrames[0].ID)
	assert.Equal(t, "main.main", stackFrames[0].Name)
	assert.NotNil(t, stackFrames[0].Path)
	assert.Equal(t, 33, stackFrames[0].Line)
	assert.Equal(t, "1", stackFrames[1].ID)
	assert.Equal(t, "runtime.main", stackFrames[1].Name)
	assert.NotNil(t, stackFrames[1].Path)
	assert.NotNil(t, stackFrames[1].Line)
	// 读取变量
	variables, err := debug.GetFrameVariables(ctx, "0")
	assert.Nil(t, err)
	// 基本类型读取正确
	td.JSON(t, &debugger.Variable{Name: "intVar", Type: "int", Value: utils.GetPointValue("42"), Reference: "0"}, variables[0])
	td.JSON(t, &debugger.Variable{Name: "stringVar", Type: "string", Value: utils.GetPointValue("\"Hello, World!\""), Reference: "0"}, variables[1])
	// 数组
	vs, err := debug.GetVariables(ctx, variables[2].Reference)
	td.JSON(t, vs, []*debugger.Variable{
		{Name: "[0]", Type: "int", Value: utils.GetPointValue("1"), Reference: "0"},
		{Name: "[1]", Type: "int", Value: utils.GetPointValue("2"), Reference: "0"},
		{Name: "[2]", Type: "int", Value: utils.GetPointValue("3"), Reference: "0"}})
	// 结构体
	vs, err = debug.GetVariables(ctx, variables[3].Reference)
	td.JSON(t, vs, []*debugger.Variable{
		{Name: "Field1", Type: "int", Value: utils.GetPointValue("10"), Reference: "0"},
		{Name: "Field2", Type: "string", Value: utils.GetPointValue("\"Struct Value\""), Reference: "0"}})
	// int指针
	td.JSON(t, debugger.Variable{Name: "intPtr", Type: "*int", Value: utils.GetPointValue("*42"), Reference: "1003"}, *variables[4])
	// string指针
	td.JSON(t, debugger.Variable{Name: "stringPtr", Type: "*string", Value: utils.GetPointValue("*\"Hello, World!\""), Reference: "1004"}, *variables[5])
	// 结构体指针
	vs, err = debug.GetVariables(ctx, variables[6].Reference)
	vs, err = debug.GetVariables(ctx, vs[0].Reference)
	td.JSON(t, vs, []*debugger.Variable{
		{Name: "Field1", Type: "int", Value: utils.GetPointValue("10"), Reference: "0"},
		{Name: "Field2", Type: "string", Value: utils.GetPointValue("\"Struct Value\""), Reference: "0"}})
	// 切片
	vs, err = debug.GetVariables(ctx, variables[7].Reference)
	td.JSON(t, vs, []*debugger.Variable{
		{Name: "[0]", Type: "int", Value: utils.GetPointValue("4"), Reference: "0"},
		{Name: "[1]", Type: "int", Value: utils.GetPointValue("5"), Reference: "0"},
		{Name: "[2]", Type: "int", Value: utils.GetPointValue("6"), Reference: "0"}})
	// map
	vs, err = debug.GetVariables(ctx, variables[8].Reference)
	td.JSON(t, vs, []*debugger.Variable{
		{Name: "\"one\"", Type: "string: int", Value: utils.GetPointValue("1"), Reference: ""},
		{Name: "\"two\"", Type: "string: int", Value: utils.GetPointValue("2"), Reference: ""}})
}
