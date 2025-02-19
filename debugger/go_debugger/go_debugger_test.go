package go_debugger

import (
	"context"
	"github.com/fansqz/go-debugger/constants"
	"github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/utils"
	"github.com/stretchr/testify/assert"
	"os"
	"path"
	"testing"
)

func testGoDebugger_Run(t *testing.T) {
	var cha = make(chan interface{}, 10)
	executePath := getExecutePath("/var/fanCode/tempDir")
	defer os.RemoveAll(executePath)
	// 保存用户代码到用户的执行路径，并获取编译文件列表
	debug := NewGoDebugger()
	ctx := context.Background()
	err := debug.Start(ctx, &debugger.StartOption{
		WorkPath:     executePath,
		CompileFiles: saveUserCode(t, "./test_file/go_test1", executePath),
		BreakPoints:  []*debugger.Breakpoint{{"/main.go", 7}},
		Callback:     func(data interface{}) { cha <- data },
	})
	assert.Nil(t, err)
	// 程序启动
	assert.Equal(t, debugger.CompileSuccessEvent, <-cha)
	assert.Equal(t, debugger.LaunchSuccessEvent, <-cha)
	assert.Equal(t, &debugger.BreakpointEvent{
		Reason:      constants.NewType,
		Breakpoints: []*debugger.Breakpoint{{"/main.go", 7}},
	}, <-cha)
	assert.Equal(t, &debugger.ContinuedEvent{}, <-cha)
	// 输入并到达第一个断点
	err = debug.Send(ctx, "10\n")
	assert.Nil(t, err)
	assert.Equal(t, &debugger.StoppedEvent{Reason: constants.BreakpointStopped, File: "/main.go", Line: 7}, <-cha)
	// 进入函数内部
	err = debug.StepIn(ctx)
	assert.Nil(t, err)
	assert.Equal(t, &debugger.ContinuedEvent{}, <-cha)
	assert.Equal(t, &debugger.StoppedEvent{Reason: constants.StepStopped, File: "/main.go", Line: 12}, <-cha)
	// 跳出函数内部
	err = debug.StepOut(ctx)
	assert.Nil(t, err)
	assert.Equal(t, &debugger.ContinuedEvent{}, <-cha)
	assert.Equal(t, &debugger.StoppedEvent{Reason: constants.StepStopped, File: "/main.go", Line: 7}, <-cha)
	// 继续直到程序结束
	err = debug.Continue(ctx)
	assert.Nil(t, err)
	assert.Equal(t, &debugger.ContinuedEvent{}, <-cha)
	assert.Equal(t, &debugger.OutputEvent{Output: "a + b = 12"}, <-cha)
	assert.Equal(t, &debugger.ExitedEvent{ExitCode: 0}, <-cha)
}

// getExecutePath 给用户的此次运行生成一个临时目录
func getExecutePath(tempDir string) string {
	uuid := utils.GetUUID()
	executePath := path.Join(tempDir, uuid)
	return executePath
}

func saveUserCode(t *testing.T, file string, executePath string) []string {
	err := os.MkdirAll(executePath, os.ModePerm)
	assert.Nil(t, err)
	code, err := os.ReadFile(file)
	assert.Nil(t, err)
	err = os.WriteFile(path.Join(executePath, "main.go"), code, 0644)
	assert.Nil(t, err)
	// 将main文件进行编译即可
	return []string{path.Join(executePath, "main.go")}
}
