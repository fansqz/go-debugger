package c_debugger

import (
	"context"
	"errors"
	"fmt"
	"github.com/fansqz/go-debugger/constants"
	. "github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/debugger/c_debugger/gdb"
	"github.com/fansqz/go-debugger/utils/gosync"
	"github.com/google/go-dap"
	"log"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	OptionTimeout = time.Second * 10
)

type CDebugger struct {
	startOption *StartOption

	gdb *gdb.Gdb

	// 引用工具
	referenceUtil *ReferenceUtil

	// 事件产生时，触发该回调
	callback NotificationCallback

	// 调试的状态管理
	statusManager *StatusManager

	// gdb输出工具，用于处理gdb输出
	gdbOutputUtil *GDBOutputUtil

	// 断点记录
	mutex             sync.RWMutex
	breakpointNumbers []string

	initBreakpointCountLock sync.Mutex
	NotInitBreakpointCount  int
	breakpointInitChannel   chan struct{}

	// 由于为了防止stepIn操作会进入系统依赖内部的特殊处理
	preAction               string // 记录gdb上一个命令
	skipContinuedEventCount int64  //记录需要跳过continue事件的数量，读写时必须加锁
}

func NewCDebugger() *CDebugger {
	d := &CDebugger{
		statusManager:         NewStatusManager(),
		breakpointInitChannel: make(chan struct{}, 2),
		gdbOutputUtil:         NewGDBOutputUtil(),
		referenceUtil:         NewReferenceUtil(),
	}
	return d
}

func (g *CDebugger) Start(option *StartOption) error {
	// 设置并创建gdb
	g.callback = option.Callback
	g.startOption = option
	gd, err := gdb.New(g.gdbNotificationCallback)
	if err != nil {
		log.Printf("Start fail, err = %s\n", err)
		return err
	}
	g.gdb = gd
	// 创建命令
	m, _ := g.gdb.Send("file-exec-and-symbols", option.ExecFile)
	if result, ok := m["class"]; ok && result == "done" {
		return nil
	} else {
		return fmt.Errorf("目标代码加载失败")
	}
}

// Run 同步方法，开始运行
func (g *CDebugger) Run() error {
	var gdbCallback gdb.AsyncCallback = func(m map[string]interface{}) {
		gosync.Go(context.Background(), g.processUserInput)
		// 启动协程读取用户输出
		gosync.Go(context.Background(), g.processUserOutput)
	}
	g.preAction = "exec-run"
	if err := g.gdb.SendAsync(gdbCallback, "exec-run"); err != nil {
		log.Printf("Run fail, err = %s\n", err)
		// 启动失败
		return err
	}
	return nil
}

// processUserInput 循环读取用户输入
func (g *CDebugger) processUserInput(ctx context.Context) {
	var input string
	for {
		_, err := fmt.Scanln(&input)
		if g.statusManager.Is(Finish) {
			break
		}
		if err == nil {
			log.Printf("processUserInput, err = %s\n", err)
			if input[len(input)-1] != '\n' {
				input = input + "\n"
			}
			g.gdb.Write([]byte(input))
		}
	}
}

// processUserOutput 循环处理用户输出
func (g *CDebugger) processUserOutput(ctx context.Context) {
	b := make([]byte, 1024)
	for {
		n, err := g.gdb.Read(b)
		if err != nil {
			return
		}
		output := string(b[0:n])
		fmt.Print(output)
	}
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

func (g *CDebugger) SetBreakpoints(source dap.Source, breakpoints []dap.SourceBreakpoint) error {
	g.mutex.Lock()
	// 删除原来的所有断点
	g.removeBreakpoints(g.breakpointNumbers)
	for _, bp := range breakpoints {
		result, err := g.gdb.SendWithTimeout(OptionTimeout, "break-insert", source.Path+":"+strconv.Itoa(bp.Line))
		if err != nil {
			continue
		} else {
			success, number := g.gdbOutputUtil.parseAddBreakpointOutput(result)
			if success {
				g.breakpointNumbers = append(g.breakpointNumbers, number)
			}
		}
	}
	g.mutex.Unlock()
	return nil
}

func (g *CDebugger) removeBreakpoints(numbers []string) error {
	for _, number := range numbers {
		var callback gdb.AsyncCallback = func(m map[string]interface{}) {}
		g.gdb.SendAsync(callback, "break-delete", number)
	}
	return nil
}

func (g *CDebugger) GetStackTrace() ([]dap.StackFrame, error) {
	if !g.statusManager.Is(Stopped) {
		return nil, errors.New("程序未暂停无法获取栈帧信息")
	}
	m, err := g.sendWithTimeOut(OptionTimeout, "stack-list-frames")
	if err != nil {
		log.Printf("GetStackTrace fail, err = %s\n", err)
		return nil, err
	}
	return g.gdbOutputUtil.parseStackTraceOutput(m), nil
}

func (g *CDebugger) GetScopes(frameId int) ([]dap.Scope, error) {
	// 读取栈帧
	return []dap.Scope{
		{Name: "Global", VariablesReference: globalScopeReference},
		{Name: "Local", VariablesReference: g.referenceUtil.GetScopesReference(frameId)},
	}, nil
}

func (g *CDebugger) GetVariables(reference int) ([]dap.Variable, error) {
	if !g.statusManager.Is(Stopped) {
		return nil, errors.New("程序未暂停变量信息")
	}
	// 通过scope引用获取变量列表
	if g.referenceUtil.CheckIsGlobalScope(reference) {
		return g.getGlobalScopeVariables()
	}
	if g.referenceUtil.CheckIsLocalScope(reference) {
		return g.getLocalScopeVariables(reference)
	}
	return g.getVariables(reference)
}

func (g *CDebugger) getVariables(reference int) ([]dap.Variable, error) {
	// 解析引用
	refStruct, err := g.referenceUtil.ParseVariableReference(reference)
	if err != nil {
		log.Printf("getVariables failed: %v\n", err)
		return nil, err
	}

	// 如果是普通类型需要切换栈帧，同一个变量名，可能在不同栈帧中会有重复，需要定位栈帧和变量名称才能读取到变量值。
	if refStruct.Type == "v" {
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
		log.Printf("getVariables failed: %v\n", err)
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
		log.Printf("getVariables fail, err = %s\n", err)
		return nil, err
	}
	answer := make([]dap.Variable, 0, 10)
	variables := g.gdbOutputUtil.parseVariablesOutput(strconv.Itoa(reference), m)
	for _, variable := range variables {
		// 如果value为空说明是结构体类型
		if variable.Value == "" {
			// 已经定位了的结构体下的某个属性，直接加路径即可。
			variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(GetFieldReferenceStruct(refStruct, variable.Name))
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if variable.Value != "" && variable.IndexedVariables != 0 {
			if variable.Type != "char *" {
				if g.gdbOutputUtil.isShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := g.gdbOutputUtil.convertValueToAddress(variable.Value)
				variable.Value = address
				if !g.gdbOutputUtil.isNullPoint(address) {
					variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
						&ReferenceStruct{
							Type:         "p",
							PointType:    variable.Type,
							Address:      address,
							VariableName: variable.Name})
				}
			}
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

func (g *CDebugger) getLocalScopeVariables(reference int) ([]dap.Variable, error) {
	frameId := g.referenceUtil.GetFrameIDByLocalReference(reference)
	// 获取当前线程id
	currentThreadId, _ := g.getCurrentThreadId()
	// 获取栈帧中所有局部变量
	m, err := g.sendWithTimeOut(OptionTimeout, "stack-list-variables",
		"--thread", currentThreadId, "--frame", strconv.Itoa(frameId), "2")
	if err != nil {
		log.Printf("getLocalScopeVariables failed: %v\n", err)
		return nil, err
	}

	var answer []dap.Variable
	variables := g.gdbOutputUtil.parseFrameVariablesOutput(m)
	for _, variable := range variables {
		// 结构体类型，如果value为空说明是结构体类型
		if variable.Value == "" && g.getChildrenNumber(variable.Name) != 0 {
			// 如果parentRef不为空，说明是栈帧中的某个结构体变量
			variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
				&ReferenceStruct{
					Type:         "v",
					FrameId:      strconv.Itoa(frameId),
					VariableName: variable.Name,
				})
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if variable.Value != "" && g.getChildrenNumber(variable.Name) != 0 {
			if variable.Type != "char *" {
				if g.gdbOutputUtil.isShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := g.gdbOutputUtil.convertValueToAddress(variable.Value)
				variable.Value = address
				if !g.gdbOutputUtil.isNullPoint(address) {
					variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
						&ReferenceStruct{
							Type:         "p",
							PointType:    variable.Type,
							Address:      address,
							VariableName: variable.Name,
						})
				}
			}
		}
		// 如果是数组类型，设置value为数组的首地址
		addr, err := g.checkAndSetArrayAddress(variable)
		if err != nil {
			log.Printf("checkAndSetArrayAddress failed: %v\n", err)
		} else if addr != "" {
			variable.Value = addr
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

func (g *CDebugger) getGlobalScopeVariables() ([]dap.Variable, error) {
	m, err := g.sendWithTimeOut(OptionTimeout, "symbol-info-variables", "--max-results", "20")
	if err != nil {
		return nil, err
	}
	variables := g.gdbOutputUtil.parseGlobalVariableOutput(m)
	var answer []dap.Variable
	// 遍历所有的answer
	for _, variable := range variables {
		// 读取变量值
		m, err = g.sendWithTimeOut(OptionTimeout, "data-evaluate-expression", variable.Name)
		if err != nil {
			log.Printf("getGlobalScopeVariables fail, err = %s\n", err)
			return nil, err
		}
		payload := g.gdbOutputUtil.getInterfaceFromMap(m, "payload")
		variable.Value = g.gdbOutputUtil.getStringFromMap(payload, "value")
		// 结构体类型，如果value为空说明是结构体类型
		if !g.gdbOutputUtil.checkIsAddress(variable.Value) && g.getChildrenNumber(variable.Name) != 0 {
			// 如果parentRef不为空，说明是栈帧中的某个结构体变量
			variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
				&ReferenceStruct{
					Type:         "v",
					FrameId:      "0",
					VariableName: variable.Name,
				})
			variable.Value = ""
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if g.gdbOutputUtil.checkIsAddress(variable.Value) && g.getChildrenNumber(variable.Name) != 0 {
			if variable.Type != "char *" {
				if g.gdbOutputUtil.isShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := g.gdbOutputUtil.convertValueToAddress(variable.Value)
				variable.Value = address
				if !g.gdbOutputUtil.isNullPoint(address) {
					variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
						&ReferenceStruct{
							Type:         "p",
							PointType:    variable.Type,
							Address:      address,
							VariableName: variable.Name,
						})
				}
			}
		}
		// 如果是数组类型，设置value为数组的首地址
		addr, err := g.checkAndSetArrayAddress(variable)
		if err != nil {
			log.Printf("checkAndSetArrayAddress failed: %v\n", err)
		} else {
			variable.Value = addr
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
		log.Printf("Terminate fail, err = %s\n", err)
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

func (g *CDebugger) checkAndSetArrayAddress(variable dap.Variable) (string, error) {
	pattern := `\w+\s*\[\d*\]`
	re, err := regexp.Compile(pattern)
	if err != nil {
		log.Printf("checkAndSetArrayAddress fail, err = %s\n", err)
		return "", err
	}
	if re.MatchString(variable.Type) {
		// 如果是类型是数组类型，需要设置value为地址，用于数组可视化
		m, err := g.sendWithTimeOut(OptionTimeout, "data-evaluate-expression", "&"+variable.Name)
		if err != nil {
			log.Printf("checkAndSetArrayAddress fail, err = %s\n", err)
			return "", err
		}
		payload := g.gdbOutputUtil.getInterfaceFromMap(m, "payload")
		return g.gdbOutputUtil.getStringFromMap(payload, "value"), nil
	}
	return "", nil
}

// getCurrentThreadId 获取当前线程id
func (g *CDebugger) getCurrentThreadId() (string, error) {
	// 获取当前线程id
	m, err := g.sendWithTimeOut(OptionTimeout, "thread-info")
	if err != nil {
		log.Printf("getCurrentThreadId fail, err = %s\n", err)
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
			g.statusManager.Set(Stopped)
		case "running":
			g.processRunningData()
			g.statusManager.Set(Running)
		}
	}

}

// processStoppedData 处理gdb返回的stopped数据，程序停止到程序的某个位置就会返回stopped data
func (g *CDebugger) processStoppedData(m interface{}) {
	stoppedOutput := g.gdbOutputUtil.parseStoppedEventOutput(m)
	// 停留在断点
	if stoppedOutput.reason == constants.StepStopped || stoppedOutput.reason == constants.BreakpointStopped {
		// 返回停留的断点位置
		g.callback(&dap.StoppedEvent{
			Event: *NewEvent(0, "stopped"),
			Body:  dap.StoppedEventBody{Reason: string(stoppedOutput.reason)},
		})
	}
	if stoppedOutput.reason == constants.ExitedNormally {
		// 程序退出
		g.callback(&dap.TerminatedEvent{
			Event: *NewEvent(0, "terminated"),
		})
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
	g.callback(&dap.ContinuedEvent{
		Event: *NewEvent(0, "continued"),
		Body:  dap.ContinuedEventBody{},
	})
	// 设置用户程序为执行状态
	g.statusManager.Set(Running)
}

// getChildrenNumber 获取children数量
func (g *CDebugger) getChildrenNumber(name string) int {
	_, _ = g.sendWithTimeOut(OptionTimeout, "var-create", name, "@", name)
	defer func() {
		_, _ = g.sendWithTimeOut(OptionTimeout, "var-delete", name)
	}()
	m, err := g.sendWithTimeOut(OptionTimeout, "var-info-num-children", name)
	if err != nil {
		log.Printf("getChildrenNumber fail, err = %s\n", err)
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
