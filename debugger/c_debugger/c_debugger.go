package c_debugger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/fansqz/go-debugger/constants"
	. "github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/debugger/c_debugger/gdb"
	"github.com/sirupsen/logrus"
	"log"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	CompileLimitTime = int64(10 * time.Second)
	OptionTimeout    = time.Second * 2
)

type CDebugger struct {
	startOption *StartOption

	gdb *gdb.Gdb

	// 事件产生时，触发该回调
	callback NotificationCallback

	// 调试的状态管理
	statusManager *StatusManager

	// gdb输出工具，用于处理gdb输出
	gdbOutputUtil *GDBOutputUtil

	initBreakpointCountLock sync.Mutex
	NotInitBreakpointCount  int
	breakpointInitChannel   chan struct{}

	// 由于为了防止stepIn操作会进入系统依赖内部的特殊处理
	preAction               string // 记录gdb上一个命令
	skipContinuedEventCount int64  //记录需要跳过continue事件的数量，读写时必须加锁
}

func NewGdbDebugger() *CDebugger {
	d := &CDebugger{
		statusManager:         NewStatusManager(),
		breakpointInitChannel: make(chan struct{}, 2),
	}
	return d
}

func (g *CDebugger) Start(option *StartOption) error {
	// 设置并创建gdb
	g.gdbOutputUtil = NewGDBOutputUtil(option.WorkPath)
	g.callback = option.Callback
	gd, err := gdb.New(g.gdbNotificationCallback)
	if err != nil {
		logrus.Error(err)
		return err
	}
	g.gdb = gd
	// 编译并启动用户程序
	go g.start()
	return nil
}

// launch 同步 编译并启动gdb
func (g *CDebugger) start() {
	// 设置gdb文件
	execFile := path.Join(g.startOption.WorkPath, "main")
	// 进行编译
	err := g.Compile(g.startOption.CompileFiles, execFile, g.startOption)
	if err != nil {
		g.callback(NewCompileEvent(false, err.Error()))
	}
	g.callback(NewCompileEvent(true, "用户代码编译成功"))
	// 创建命令
	m, _ := g.gdb.Send("file-exec-and-symbols", execFile)
	if result, ok := m["class"]; ok && result == "done" {
		g.callback(NewLaunchEvent(true, "目标代码加载成功"))
		// 初始化断点
		g.initBreakPoint(g.startOption.BreakPoints)
		if err = g.execRun(); err != nil {
			g.statusManager.Set(Finish)
		}
	} else {
		g.callback(NewLaunchEvent(false, "目标代码加载失败"))
		g.statusManager.Set(Finish)
	}
}

func (g *CDebugger) Compile(compileFiles []string, outFilePath string, options *StartOption) error {
	var ctx context.Context
	compileFiles = append([]string{"-g", "-o", outFilePath}, compileFiles...)
	// 创建一个带有超时时间的上下文
	var cancel context.CancelFunc
	if options != nil && options.CompileTimeout != 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(options.CompileTimeout))
		defer cancel()
	} else {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "gcc", compileFiles...)
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
			errMessage = maskPath(options.WorkPath, string(errBytes))
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

// initBreakPoint 同步方法，初始化断点
func (g *CDebugger) initBreakPoint(breakpoints []*Breakpoint) {
	if len(breakpoints) == 0 {
		return
	}
	g.initBreakpointCountLock.Lock()
	g.NotInitBreakpointCount += len(breakpoints)
	g.initBreakpointCountLock.Unlock()
	if err := g.AddBreakpoints(breakpoints); err != nil {
		logrus.Errorf("[startTask] AddBreakpoints error, err = %v", err)
	}
	// 等待10s,断点全部添加成功
	select {
	case <-g.breakpointInitChannel:
	case <-time.After(time.Second * 10):
	}
}

// execRun 同步方法，开始运行
func (g *CDebugger) execRun() error {
	var gdbCallback gdb.AsyncCallback = func(m map[string]interface{}) {
		// 启动协程读取用户输出
		go g.processUserOutput()
	}
	g.preAction = "exec-run"
	if err := g.gdb.SendAsync(gdbCallback, "exec-run"); err != nil {
		// 启动失败
		logrus.Errorf("[startTask] start debug error, err = %v", err)
		return err
	}
	return nil
}

// processUserOutput 循环处理用户输出
func (g *CDebugger) processUserOutput() {
	b := make([]byte, 1024)
	for {
		n, err := g.gdb.Read(b)
		if err != nil {
			log.Println(err)
			return
		}
		output := string(b[0:n])
		g.callback(NewOutputEvent(output))
	}
}

// Send 输入
func (g *CDebugger) Send(input string) error {
	// 向伪终端主设备发送数据
	if _, err := g.gdb.Write([]byte(input)); err != nil {
		log.Println(err)
		return err
	}
	return nil
}

func (g *CDebugger) StepOver() error {
	if !g.statusManager.Is(Stopped) {
		return errors.New("程序运行中，无法执行单步调试")
	}
	return g.stepOver()
}

func (g *CDebugger) stepOver() error {
	g.preAction = "exec-next"
	err := g.gdb.SendAsync(func(obj map[string]interface{}) {}, "exec-next")
	return err
}

func (g *CDebugger) StepIn() error {
	if !g.statusManager.Is(Stopped) {
		return errors.New("程序运行中，无法执行单步调试")
	}
	return g.stopIn()
}

func (g *CDebugger) stopIn() error {
	g.preAction = "exec-step"
	err := g.gdb.SendAsync(func(obj map[string]interface{}) {}, "exec-step")
	return err
}

func (g *CDebugger) StepOut() error {
	if !g.statusManager.Is(Stopped) {
		return errors.New("程序运行中，无法执行单步调试")
	}
	return g.stopOut()
}

func (g *CDebugger) stopOut() error {
	g.preAction = "exec-finish"
	err := g.gdb.SendAsync(func(obj map[string]interface{}) {}, "exec-finish")
	return err
}

func (g *CDebugger) Continue() error {
	if !g.statusManager.Is(Stopped) {
		return errors.New("程序运行中，无法执行continue")
	}
	return g.continue2()
}

func (g *CDebugger) continue2() error {
	g.preAction = "exec-continue"
	err := g.gdb.SendAsync(func(obj map[string]interface{}) {}, "exec-continue")
	return err
}

func (g *CDebugger) AddBreakpoints(breakpoints []*Breakpoint) error {
	var callback gdb.AsyncCallback = func(m map[string]interface{}) {
		// 解析断点输出，并响应用户哪些断点已经被添加
		if success, bps := g.gdbOutputUtil.parseAddBreakpointOutput(m); success {
			g.initBreakpointCountLock.Lock()
			if g.NotInitBreakpointCount > 0 {
				g.NotInitBreakpointCount--
				// 通知断点初始化完成
				if g.NotInitBreakpointCount == 0 {
					g.breakpointInitChannel <- struct{}{}
				}
			}
			g.initBreakpointCountLock.Unlock()
			g.callback(NewBreakpointEvent(constants.NewType, bps))
		}
	}
	for _, bp := range breakpoints {
		if err := g.gdb.SendAsync(callback, "break-insert", path.Join(g.startOption.WorkPath, bp.File)+":"+strconv.Itoa(bp.Line)); err != nil {
			logrus.Error(err)
		}
	}
	return nil
}

func (g *CDebugger) RemoveBreakpoints(breakpoints []*Breakpoint) error {
	for _, bp := range breakpoints {
		file := bp.File
		if file[0] != '/' {
			file = "/" + file
		}
		number := g.gdbOutputUtil.getBreakPointNumber(file, bp.Line)
		var callback gdb.AsyncCallback = func(m map[string]interface{}) {
			// 断点删除成功，则移除map中的断点记录，并响应结果
			if g.gdbOutputUtil.parseRemoveBreakpointOutput(m) {
				// 移除断点记录并响应
				g.gdbOutputUtil.removeBreakPoint(file, bp.Line)
				g.callback(NewBreakpointEvent(constants.RemovedType, []*Breakpoint{{file, bp.Line}}))
			}
		}
		if err := g.gdb.SendAsync(callback, "break-delete", number); err != nil {
			logrus.Errorf("[RemoveBreakpoints] remove breakpoint fail, err = %v", err)
		}
	}
	return nil
}

func (g *CDebugger) GetStackTrace() ([]*StackFrame, error) {
	if !g.statusManager.Is(Stopped) {
		return nil, errors.New("程序未暂停无法获取栈帧信息")
	}
	m, err := g.sendWithTimeOut(OptionTimeout, "stack-list-frames")
	if err != nil {
		return nil, err
	}
	return g.gdbOutputUtil.parseStackTraceOutput(m), nil
}

func (g *CDebugger) GetFrameVariables(frameId string) ([]*Variable, error) {
	if !g.statusManager.Is(Stopped) {
		return nil, errors.New("程序未暂停变量信息")
	}
	// 获取当前线程id
	currentThreadId, err := g.getCurrentThreadId()
	if err != nil {
		return nil, err
	}
	// 获取栈帧中所有局部变量
	m, err := g.sendWithTimeOut(OptionTimeout, "stack-list-variables",
		"--thread", currentThreadId, "--frame", frameId, "2")
	if err != nil {
		return nil, err
	}
	var answer []*Variable
	variables := g.gdbOutputUtil.parseFrameVariablesOutput(m)
	for _, variable := range variables {
		// 结构体类型，如果value为空说明是结构体类型
		if variable.Value == nil && g.getChildrenNumber(variable.Name) != 0 {
			// 如果parentRef不为空，说明是栈帧中的某个结构体变量
			variable.Reference = convertReference(&referenceStruct{Type: "v", FrameId: frameId, VariableName: variable.Name})
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if variable.Value != nil && g.getChildrenNumber(variable.Name) != 0 {
			if variable.Type != "char *" {
				if g.gdbOutputUtil.isShouldBeFilterAddress(*variable.Value) {
					continue
				}
				address := g.gdbOutputUtil.convertValueToAddress(*variable.Value)
				if !g.gdbOutputUtil.isNullPoint(address) {
					variable.Reference = convertReference(
						&referenceStruct{Type: "p", PointType: variable.Type, Address: address, VariableName: variable.Name})
				}
			}
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

func (g *CDebugger) GetVariables(reference string) ([]*Variable, error) {
	if !g.statusManager.Is(Stopped) {
		return nil, errors.New("程序未暂停变量信息")
	}
	// 正则表达式，捕获栈帧ID和变量名
	refStruct, err := parseReference(reference)
	if err != nil {
		return nil, err
	}
	if refStruct.Type == "v" {
		// 如果是普通类型需要切换栈帧，同一个变量名，可能在不同栈帧中会有重复，需要定位栈帧和变量名称才能读取到变量值。
		if _, err = g.sendWithTimeOut(OptionTimeout, "stack-select-frame", refStruct.FrameId); err != nil {
			return nil, err
		}
	}

	// 获取所有children列表并解析
	var m map[string]interface{}

	name := "structName"
	// 创建变量
	if refStruct.Type == "v" {
		m, err = g.sendWithTimeOut(OptionTimeout, "var-create", name, "@",
			refStruct.VariableName)
	} else if refStruct.Type == "p" {
		m, err = g.sendWithTimeOut(OptionTimeout, "var-create", name, "*",
			fmt.Sprintf("(%s)%s", refStruct.PointType, refStruct.Address))
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = g.sendWithTimeOut(OptionTimeout, "var-delete", "structName")
	}()

	if refStruct.FieldPath == "" {
		m, err = g.sendWithTimeOut(OptionTimeout, "var-list-children", "1",
			name)
	} else {
		m, err = g.sendWithTimeOut(OptionTimeout, "var-list-children", "1",
			fmt.Sprintf("%s.%s", name, refStruct.FieldPath))
	}
	if err != nil {
		return nil, err
	}
	answer := make([]*Variable, 0, 10)
	variables := g.gdbOutputUtil.parseVariablesOutput(reference, m)
	for _, variable := range variables {
		// 如果value为空说明是结构体类型
		if variable.Value == nil {
			// 已经定位了的结构体下的某个属性，直接加路径即可。
			variable.Reference = getFieldReference(reference, variable.Name)
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if variable.Value != nil && variable.ChildrenNumber != 0 {
			if variable.Type != "char *" {
				if g.gdbOutputUtil.isShouldBeFilterAddress(*variable.Value) {
					continue
				}
				address := g.gdbOutputUtil.convertValueToAddress(*variable.Value)
				if !g.gdbOutputUtil.isNullPoint(address) {
					variable.Reference = convertReference(
						&referenceStruct{Type: "p", PointType: variable.Type, Address: address, VariableName: variable.Name})
				}
			}
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

func (g *CDebugger) Terminate() error {
	if g.statusManager.Is(Finish) {
		return nil
	}
	// 发送终端给程序
	err := g.gdb.Interrupt()
	if err != nil {
		logrus.Errorf("[Terminate] interrupt error, err = %v", err)
		return err
	}
	_ = g.gdb.Exit()
	// 保证map的线程安全
	g.gdbOutputUtil.lock.Lock()
	defer g.gdbOutputUtil.lock.Unlock()
	g.skipContinuedEventCount = 0
	g.statusManager.Set(Finish)
	return nil
}

// getCurrentThreadId 获取当前线程id
func (g *CDebugger) getCurrentThreadId() (string, error) {
	// 获取当前线程id
	m, err := g.sendWithTimeOut(OptionTimeout, "thread-info")
	if err != nil {
		return "", err
	}
	threadMap, success := g.gdbOutputUtil.getPayloadFromMap(m)
	if !success {
		return "", errors.New("获取线程id失败")
	}
	currentThreadId := g.gdbOutputUtil.getStringFromMap(threadMap, "current-thread-id")
	return currentThreadId, nil
}

// gdbNotificationCallback 处理gdb异步响应的回调
func (g *CDebugger) gdbNotificationCallback(m map[string]interface{}) {
	typ := g.gdbOutputUtil.getStringFromMap(m, "type")
	switch typ {
	case "exec":
		class := g.gdbOutputUtil.getStringFromMap(m, "class")
		switch class {
		case "stopped":
			// 处理程序停止的事件
			g.processStoppedData(g.gdbOutputUtil.getInterfaceFromMap(m, "payload"))
		case "running":
			g.processRunningData()
		case "log":
			logrus.Error(m)
			g.processLogData(g.gdbOutputUtil.getInterfaceFromMap(m, "payload"))
		}
	}

}

// processStoppedData 处理gdb返回的stopped数据，程序停止到程序的某个位置就会返回stopped data
func (g *CDebugger) processStoppedData(m interface{}) {
	var err error
	stoppedOutput := g.gdbOutputUtil.parseStoppedEventOutput(m)
	if stoppedOutput == nil {
		logrus.Errorf("[processStoppedData] processStoppedData fail, data = %v)", m)
	}
	// 停留在断点
	if stoppedOutput.reason == constants.BreakpointStopped {
		// 返回停留的断点位置
		g.callback(NewStoppedEvent(stoppedOutput.reason, stoppedOutput.file, stoppedOutput.line))
		// 标记程序停止
		g.statusManager.Set(Stopped)
	}
	// 使用单步调试产生的stopped
	if stoppedOutput.reason == constants.StepStopped {
		if !strings.HasPrefix(stoppedOutput.fullname, g.startOption.WorkPath) {
			if g.preAction == "exec-step" {
				// 如果是由于gdb单步调试进入函数内部导致逃离了workpath，则使用stepout()跳出程序
				// 由于这个时候程序还不能停下，所以不会去设置running = false
				if err = g.stopOut(); err == nil {
					atomic.AddInt64(&g.skipContinuedEventCount, 1)
					return
				}
				logrus.Errorf("[processStoppedData] stepout fail, err = %v", err)
			} else {
				// 其他原因逃离工作目录，直接continue
				// 由于这个时候程序还不能停下，所以不会去设置running = false
				if err = g.continue2(); err == nil {
					atomic.AddInt64(&g.skipContinuedEventCount, 1)
					return
				}
				logrus.Errorf("[processStoppedData] continue2 fail, err = %v", err)
			}
		}
		// 返回停留的断点位置
		g.callback(NewStoppedEvent(stoppedOutput.reason, stoppedOutput.file, stoppedOutput.line))
		// 标记程序停止
		g.statusManager.Set(Stopped)
	}

	if stoppedOutput.reason == constants.ExitedNormally {
		// 程序退出
		g.callback(NewExitedEvent(0, ""))
		// 标记程序停止
		g.statusManager.Set(Stopped)
	}
}

// processRunningData 处理gdb返回的running事件
func (g *CDebugger) processRunningData() {
	// 程序执行，如果有需要跳过的continue事件，则跳过
	skipCount := atomic.LoadInt64(&g.skipContinuedEventCount)
	if skipCount > 0 {
		atomic.AddInt64(&g.skipContinuedEventCount, -1)
		return
	}
	g.callback(NewContinuedEvent())
	// 设置用户程序为执行状态
	g.statusManager.Set(Running)
}

// processLogData 处理gdb返回的log事件
func (g *CDebugger) processLogData(m interface{}) {
}

// getChildrenNumber 获取children数量
func (g *CDebugger) getChildrenNumber(name string) int {
	_, _ = g.sendWithTimeOut(OptionTimeout, "var-create", name, "@", name)
	defer func() {
		_, _ = g.sendWithTimeOut(OptionTimeout, "var-delete", name)
	}()
	m, err := g.sendWithTimeOut(OptionTimeout, "var-info-num-children", name)
	if err != nil {
		return 0
	}
	payload, success := g.gdbOutputUtil.getPayloadFromMap(m)
	if !success {
		return 0
	}
	return g.gdbOutputUtil.getIntFromMap(payload, "numchild")
}

func (g *CDebugger) sendWithTimeOut(timeout time.Duration, operation string, args ...string) (map[string]interface{}, error) {
	channel := make(chan map[string]interface{}, 1)

	err := g.gdb.SendAsync(func(obj map[string]interface{}) {
		channel <- obj
	}, operation, args...)
	if err != nil {
		return nil, err
	}
	select {
	case m := <-channel:
		return m, nil
	case <-time.After(timeout):
		return nil, errors.New("GetStackTrace time out")
	}
}

func maskPath(workPath string, message string) string {
	if message == "" {
		return ""
	}
	if filepath.IsAbs(workPath) && filepath.IsAbs("./") {
		relativePath := "." + string(filepath.Separator)
		absolutePath := filepath.Join(workPath, relativePath)
		message = strings.Replace(message, relativePath, absolutePath, -1)
	}
	repl := ""
	if workPath[len(workPath)-1] == '/' {
		repl = "/"
	}
	pattern := regexp.QuoteMeta(workPath)
	re := regexp.MustCompile(pattern)
	message = re.ReplaceAllString(message, repl)
	return message
}
