package go_debugger

import (
	"context"
	"github.com/fansqz/go-debugger/constants"
	"github.com/fansqz/go-debugger/debugger"
	"github.com/maxatome/go-testdeep/td"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func testGoDebugger_Visual1(t *testing.T) {
	var cha = make(chan interface{}, 10)
	executePath := getExecutePath("/var/fanCode/tempDir")
	defer os.RemoveAll(executePath)
	// 保存用户代码到用户的执行路径，并获取编译文件列表
	debug := NewGoDebugger()
	ctx := context.Background()
	err := debug.Start(ctx, &debugger.StartOption{
		WorkPath:     executePath,
		CompileFiles: saveUserCode(t, "./test_file/go_test3", executePath),
		BreakPoints:  []*debugger.Breakpoint{{"/main.go", 39}},
		Callback:     func(data interface{}) { cha <- data },
	})
	assert.Nil(t, err)
	// 程序启动
	assert.Equal(t, debugger.CompileSuccessEvent, <-cha)
	assert.Equal(t, debugger.LaunchSuccessEvent, <-cha)
	assert.Equal(t, &debugger.BreakpointEvent{
		Reason:      constants.NewType,
		Breakpoints: []*debugger.Breakpoint{{"/main.go", 39}},
	}, <-cha)
	assert.Equal(t, &debugger.ContinuedEvent{}, <-cha)
	assert.Equal(t, &debugger.StoppedEvent{Reason: constants.BreakpointStopped, File: "/main.go", Line: 39}, <-cha)
	// 读取二叉树
	answer, err := debug.StructVisual(ctx, &debugger.StructVisualQuery{
		Struct: "TreeNode",
		Points: []string{"Left", "Right"},
		Values: []string{"Data"},
	})
	assert.Nil(t, err)
	assert.Len(t, answer.Points, 1)
	assert.Equal(t, "root", answer.Points[0].Name)
	assert.Equal(t, "*main.TreeNode", answer.Points[0].Type)
	assert.Len(t, answer.Nodes, 7)
	assert.Equal(t, "Left", answer.Nodes[0].Points[0].Name)
	assert.Equal(t, "*main.TreeNode", answer.Nodes[0].Points[0].Type)
}

func testGoDebugger_Visual2(t *testing.T) {
	var cha = make(chan interface{}, 10)
	executePath := getExecutePath("/var/fanCode/tempDir")
	defer os.RemoveAll(executePath)
	// 保存用户代码到用户的执行路径，并获取编译文件列表
	debug := NewGoDebugger()
	ctx := context.Background()
	err := debug.Start(ctx, &debugger.StartOption{
		WorkPath:     executePath,
		CompileFiles: saveUserCode(t, "./test_file/go_test4", executePath),
		BreakPoints:  []*debugger.Breakpoint{{"/main.go", 22}},
		Callback:     func(data interface{}) { cha <- data },
	})
	assert.Nil(t, err)
	// 程序启动
	assert.Equal(t, debugger.CompileSuccessEvent, <-cha)
	assert.Equal(t, debugger.LaunchSuccessEvent, <-cha)
	assert.Equal(t, &debugger.BreakpointEvent{
		Reason:      constants.NewType,
		Breakpoints: []*debugger.Breakpoint{{"/main.go", 22}},
	}, <-cha)
	assert.Equal(t, &debugger.ContinuedEvent{}, <-cha)
	assert.Equal(t, &debugger.StoppedEvent{Reason: constants.BreakpointStopped, File: "/main.go", Line: 22}, <-cha)
	// 读取二叉树
	answer, err := debug.VariableVisual(ctx, &debugger.VariableVisualQuery{
		StructVars: []string{"arr"},
		PointVars:  []string{"min", "max"},
	})
	assert.Nil(t, err)
	assert.Len(t, answer.Structs[0].Values, 9)
	td.JSON(t, answer.Points[0], &debugger.VisualVariable{Name: "max", Type: "int", Value: "88"})
	td.JSON(t, answer.Points[1], &debugger.VisualVariable{Name: "min", Type: "int", Value: "1"})
}
