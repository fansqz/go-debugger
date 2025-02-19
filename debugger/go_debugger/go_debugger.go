package go_debugger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/creack/pty"
	"github.com/fansqz/go-debugger/constants"
	. "github.com/fansqz/go-debugger/debugger"
	e "github.com/fansqz/go-debugger/error"
	"github.com/fansqz/go-debugger/utils/gosync"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
	"github.com/sirupsen/logrus"
	"golang.org/x/term"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var LoadConfig = proc.LoadConfig{
	FollowPointers:     true,
	MaxVariableRecurse: 1,
	MaxStringLen:       512,
	MaxArrayValues:     64,
	MaxStructFields:    -1,
}

type GoDebugger struct {
	startOption *StartOption

	// 事件产生时，触发该回调
	callback NotificationCallback

	// statusManager 调试的状态管理
	statusManager *StatusManager

	// 调试器
	delve *debugger.Debugger

	ptm *os.File
	pts *os.File

	variableHandles *variablesHandlesMap

	// lastCallTime delve最后响应的时间
	lastCallTime time.Time

	skipContinuedEventCount int64 //记录需要跳过continue事件的数量，读写时必须加锁
}

func NewGoDebugger() *GoDebugger {
	g := &GoDebugger{
		statusManager:   NewStatusManager(),
		variableHandles: newVariablesHandlesMap(),
	}
	return g
}

func (g *GoDebugger) Start(ctx context.Context, option *StartOption) error {
	logrus.Infof("[GoDebugger] Start")
	g.startOption = option
	g.callback = option.Callback

	// 开启调试
	gosync.Go(context.Background(), func(ctx context.Context) {
		g.start(ctx)
	})

	return nil
}

// start 同步 编译并启动gdb
func (g *GoDebugger) start(ctx context.Context) {
	// 设置gdb文件
	execFile := path.Join(g.startOption.WorkPath, "main")

	// 进行编译
	if err := g.Compile(g.startOption.CompileFiles, execFile, g.startOption); err != nil {
		g.callback(NewCompileEvent(false, err.Error()))
		return
	}
	g.callback(CompileSuccessEvent)

	// 启动一个虚拟终端
	ptm, pts, err := pty.Open()
	if err != nil {
		g.callback(LaunchFailEvent)
		logrus.Errorf("[start] pty open fail, err = %v", err)
		return
	}
	_, err = term.MakeRaw(int(ptm.Fd()))
	if err != nil {
		g.callback(LaunchFailEvent)
		logrus.Errorf("[start] pty open fail, err = %v", err)
		return
	}
	err = syscall.SetNonblock(int(ptm.Fd()), true)
	if err != nil {
		logrus.Errorf("[start] SetNonblock fail, err = %v", err)
	}
	g.ptm = ptm
	g.pts = pts

	// 创建delve实例
	deb, err := debugger.New(&debugger.Config{
		WorkingDir:  g.startOption.WorkPath,
		Backend:     "default",
		Foreground:  false,
		ExecuteKind: debugger.ExecutingGeneratedFile,
		Packages:    []string{"main"},
		TTY:         pts.Name(),
	}, append([]string{execFile}))

	if err != nil {
		logrus.Errorf("[start] fail, err = %v", err)
		g.callback(LaunchFailEvent)
		return
	}
	g.delve = deb
	g.callback(LaunchSuccessEvent)

	// 设置断点
	if err = g.AddBreakpoints(ctx, g.startOption.BreakPoints); err != nil {
		logrus.Errorf("[start] add breakpoint fail, err = %v", err)
		return
	}

	// 启动程序
	if err = g.continue2(); err != nil {
		logrus.Errorf("[start] continue fail, err = %v", err)
		g.callback(LaunchFailEvent)
		g.statusManager.Set(Finish)
		return
	}

	// 启动进程循环读取用户输出
	gosync.Go(context.Background(), func(ctx context.Context) {
		g.processUserOutput()
	})
}

func (g *GoDebugger) Compile(compileFiles []string, outFilePath string, options *StartOption) error {
	// 进行编译
	var cmd *exec.Cmd
	var ctx context.Context
	compileFiles = append([]string{"build", "-gcflags", "-l -N", "-o", outFilePath}, compileFiles...)
	var cancel context.CancelFunc
	if options != nil && options.CompileTimeout != 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(options.CompileTimeout))
		defer cancel()
	} else {
		ctx = context.Background()
	}
	cmd = exec.CommandContext(ctx, "go", compileFiles...)
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}

	var err error
	if err = cmd.Start(); err == nil {
		err = cmd.Wait()
	}
	if err != nil {
		errBytes := cmd.Stderr.(*bytes.Buffer).Bytes()
		errMessage := string(errBytes)
		if len(options.WorkPath) != 0 {
			errMessage = maskPath(string(errBytes), []string{options.WorkPath}, "/")
		}
		// 如果是由于超时导致的错误，则返回自定义错误
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("编译超时\n" + errMessage)
		}
		if len(errBytes) != 0 {
			return errors.New(errMessage)
		}
	}

	return nil
}

func maskPath(errorMessage string, excludedPaths []string, replacementPath string) string {
	if errorMessage == "" {
		return ""
	}
	// 遍历需要屏蔽的敏感路径
	for _, excludedPath := range excludedPaths {
		// 如果 excludedPath 是绝对路径，但 errorMessage 中含有相对路径 "./"，则将 "./" 替换为绝对路径
		if filepath.IsAbs(excludedPath) && filepath.IsAbs("./") {
			relativePath := "." + string(filepath.Separator)
			absolutePath := filepath.Join(excludedPath, relativePath)
			errorMessage = strings.Replace(errorMessage, relativePath, absolutePath, -1)
		}

		// 构建正则表达式，匹配包含敏感路径的错误消息
		pattern := regexp.QuoteMeta(excludedPath)
		re := regexp.MustCompile(pattern)
		errorMessage = re.ReplaceAllString(errorMessage, replacementPath)
	}

	return errorMessage
}

// processUserOutput 循环处理用户输出
func (g *GoDebugger) processUserOutput() {
	b := make([]byte, 1024)
	for {
		n, err := g.ptm.Read(b)
		if err != nil {
			log.Println(err)
			return
		}
		output := string(b[0:n])
		g.callback(NewOutputEvent(output))
	}
}

// Send 输入
func (g *GoDebugger) Send(ctx context.Context, input string) error {
	logrus.Infof("[GoDebugger] Send")
	if _, err := g.ptm.Write([]byte(input)); err != nil {
		return err
	}
	return nil
}

func (g *GoDebugger) StepOver(ctx context.Context) error {
	logrus.Infof("[GoDebugger] StepOver")
	if !g.statusManager.Is(Stopped) {
		return e.ErrProgramIsRunningOptionFail
	}
	gosync.Go(context.Background(), func(ctx context.Context) {
		g.Command(&api.DebuggerCommand{Name: api.Next})
	})
	return nil
}

func (g *GoDebugger) StepIn(ctx context.Context) error {
	logrus.Infof("[GoDebugger] StepIn")
	if !g.statusManager.Is(Stopped) {
		return e.ErrProgramIsRunningOptionFail
	}
	gosync.Go(context.Background(), func(ctx context.Context) {
		g.Command(&api.DebuggerCommand{Name: api.Step})
	})
	return nil
}

func (g *GoDebugger) StepOut(ctx context.Context) error {
	logrus.Infof("[GoDebugger] StepOut")
	if !g.statusManager.Is(Stopped) {
		return e.ErrProgramIsRunningOptionFail
	}
	return g.stepOut()
}

func (g *GoDebugger) stepOut() error {
	gosync.Go(context.Background(), func(ctx context.Context) {
		g.Command(&api.DebuggerCommand{Name: api.StepOut})
	})
	return nil
}

func (g *GoDebugger) Continue(ctx context.Context) error {
	logrus.Infof("[GoDebugger] Continue")
	if !g.statusManager.Is(Stopped) {
		return e.ErrProgramIsRunningOptionFail
	}
	gosync.Go(context.Background(), func(ctx context.Context) {
		g.Command(&api.DebuggerCommand{Name: api.Continue})
	})
	return nil
}

func (g *GoDebugger) continue2() error {
	gosync.Go(context.Background(), func(ctx context.Context) {
		g.Command(&api.DebuggerCommand{Name: api.Continue})
	})
	return nil
}

func (g *GoDebugger) AddBreakpoints(ctx context.Context, breakpoints []*Breakpoint) error {
	logrus.Infof("[GoDebugger] AddBreakpoints")
	for _, breakpoint := range breakpoints {
		bp := &api.Breakpoint{
			Name: fmt.Sprintf("%s:%d", path.Join(g.startOption.WorkPath, breakpoint.File), breakpoint.Line),
			File: path.Join(g.startOption.WorkPath, breakpoint.File),
			Line: breakpoint.Line,
		}
		b, err := g.delve.CreateBreakpoint(bp, "", nil, false)
		if err != nil {
			return err
		}
		// 返回断点事件
		bps := []*Breakpoint{
			{File: strings.Replace(b.File, g.startOption.WorkPath, "", 1), Line: b.Line},
		}
		g.callback(NewBreakpointEvent(constants.NewType, bps))
	}
	return nil
}

func (g *GoDebugger) RemoveBreakpoints(ctx context.Context, breakpoints []*Breakpoint) error {
	logrus.Infof("[GoDebugger] RemoveBreakpoints")
	for _, breakpoint := range breakpoints {
		bp := &api.Breakpoint{
			Name: fmt.Sprintf("%s:%d", path.Join(g.startOption.WorkPath, breakpoint.File), breakpoint.Line),
			File: path.Join(g.startOption.WorkPath, breakpoint.File),
			Line: breakpoint.Line,
		}
		b, err := g.delve.ClearBreakpoint(bp)
		if err != nil {
			return err
		}
		// 返回断点事件
		bps := []*Breakpoint{
			{File: strings.Replace(b.File, g.startOption.WorkPath, "", 1), Line: b.Line},
		}
		g.callback(NewBreakpointEvent(constants.RemovedType, bps))
	}
	return nil
}

func (g *GoDebugger) Terminate(ctx context.Context) error {
	logrus.Infof("[GoDebugger] Terminate")
	if g.statusManager.Is(Finish) {
		return nil
	}

	if g.delve == nil {
		return nil
	}

	// 程序在运行就先停止目标程序
	if g.delve.IsRunning() {
		g.delve.Command(&api.DebuggerCommand{Name: api.Halt}, nil, nil)
	}

	// 结束调试
	if err := g.delve.Detach(true); err != nil {
		return err
	}
	if g.ptm != nil {
		g.ptm.Close()
	}
	if g.pts != nil {
		g.pts.Close()
	}

	g.statusManager.Set(Finish)
	return nil
}

// Command 执行delve 命令
func (g *GoDebugger) Command(cmd *api.DebuggerCommand) {
	if atomic.LoadInt64(&g.skipContinuedEventCount) > 0 {
		// 跳过返回continue
		atomic.AddInt64(&g.skipContinuedEventCount, -1)
	} else {
		g.statusManager.Set(Running)
		g.callback(NewContinuedEvent())
	}

	state, _ := g.delve.Command(cmd, nil, nil)
	if state == nil {
		return
	}
	if state.Exited {
		_ = g.Terminate(context.Background())
		g.callback(NewExitedEvent(0, ""))
		return
	}
	// 如果下一个操作正在进行中，则尝试取消下一个操作
	if state.NextInProgress {
		if err := g.delve.CancelNext(); err != nil {
			logrus.Errorf("Error cancelling next: %s", err)
		}
	}

	// 如果到达某个断点
	for _, thread := range state.Threads {
		if thread.Breakpoint != nil {
			file := thread.Breakpoint.File
			line := thread.Breakpoint.Line
			// 设置程序为stopped
			g.statusManager.Set(Stopped)
			g.callback(NewStoppedEvent(constants.BreakpointStopped, strings.Replace(file, g.startOption.WorkPath, "", 1), line))
			break
		}
	}

	// 如果执行的命令单步调试
	if cmd.Name == api.Step ||
		cmd.Name == api.StepOut ||
		cmd.Name == api.Next {
		// 获取选中的 Go 协程
		goRoutine, err := g.SelectedGoRoutine()
		if err != nil {
			return
		}
		// 获取协程当前位置的文件和行号
		file := goRoutine.CurrentLoc.File
		line := goRoutine.CurrentLoc.Line

		// 如果文件不在规定目录下
		if !strings.HasPrefix(file, g.startOption.WorkPath) {
			if cmd.Name == api.Step {
				// 如果是由于gdb单步调试进入函数内部导致逃离了workpath，则使用stepout()跳出程序
				if err = g.stepOut(); err == nil {
					atomic.AddInt64(&g.skipContinuedEventCount, 1)
					return
				}
				logrus.Errorf("[processStoppedData] stepout fail, err = %v", err)
			} else {
				// 其他原因逃离工作目录，直接continue
				if err = g.continue2(); err == nil {
					atomic.AddInt64(&g.skipContinuedEventCount, 1)
					return
				}
				logrus.Errorf("[processStoppedData] continue2 fail, err = %v", err)
			}
		}

		g.statusManager.Set(Stopped)
		// 返回单步调试的事件
		g.callback(NewStoppedEvent(constants.StepStopped, strings.Replace(file, g.startOption.WorkPath, "", 1), line))
		return
	}
	// 主动判断程序是否退出，如果退出返回exited
	_, err := g.delve.Target().Valid()
	var errProcessExited proc.ErrProcessExited
	if errors.As(err, &errProcessExited) {
		_ = g.Terminate(context.Background())
		g.callback(NewExitedEvent(0, ""))
	}
}

// SelectedGoRoutine 选择当前的go协程
func (g *GoDebugger) SelectedGoRoutine() (*proc.G, error) {
	return g.selectedGoRoutine()
}

// selectedGoRoutine 未加锁
func (g *GoDebugger) selectedGoRoutine() (*proc.G, error) {
	t := g.delve.Target()
	goroutine := t.SelectedGoroutine()
	if goroutine == nil {
		routines, _, _ := proc.GoroutinesInfo(t, 0, 0)
		for _, goroutine = range routines {
			break
		}
	}

	if goroutine == nil {
		return nil, errors.New("can't find any go routines")
	}

	return goroutine, nil
}

func (g *GoDebugger) TryLockTarget() bool {
	field := reflect.ValueOf(g.delve).Elem().FieldByName("targetMutex")
	mtx := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Interface()
	return mtx.(*sync.Mutex).TryLock()
}
