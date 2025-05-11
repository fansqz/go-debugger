package gdb_debugger

import (
	"context"
	"errors"
	"fmt"
	"github.com/fansqz/go-debugger/constants"
	. "github.com/fansqz/go-debugger/debugger"
	gdb2 "github.com/fansqz/go-debugger/debugger/gdb_debugger/gdb"
	. "github.com/fansqz/go-debugger/debugger/utils"
	"github.com/fansqz/go-debugger/utils/gosync"
	"github.com/google/go-dap"
	"github.com/sirupsen/logrus"
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

type GDBDebugger struct {
	startOption *StartOption

	// gdb调试的目标语言
	language constants.LanguageType
	// gdb示例
	gdb *gdb2.Gdb

	// functionInfos 语法解析解析出来的函数内容，局部变量获取需要通过静态代码分析内容获取变量列表
	functionInfos []FunctionInfo

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

func NewGDBDebugger(languageType constants.LanguageType) *GDBDebugger {
	d := &GDBDebugger{
		statusManager:         NewStatusManager(),
		breakpointInitChannel: make(chan struct{}, 2),
		gdbOutputUtil:         NewGDBOutputUtil(),
		referenceUtil:         NewReferenceUtil(),
		language:              languageType,
	}
	return d
}

func (g *GDBDebugger) Start(option *StartOption) error {
	// 设置并创建gdb
	g.callback = option.Callback
	g.startOption = option

	// 异步进行语法解析
	if g.startOption.MainCode != "" {
		go func() {
			var err error
			g.functionInfos, err = ParseSourceFile(g.startOption.MainCode)
			if err != nil {
				logrus.Errorf("ParseSourceFile fail, err = %v", err)
			}
		}()
	}

	gd, err := gdb2.New(g.gdbNotificationCallback)
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
func (g *GDBDebugger) Run() error {
	var gdbCallback gdb2.AsyncCallback = func(m map[string]interface{}) {
		gosync.Go(context.Background(), g.processUserInput)
		// 启动协程读取用户输出
		gosync.Go(context.Background(), g.processUserOutput)
	}
	// 设置语言
	_, _ = g.gdb.SendWithTimeout(OptionTimeout, "gdb-set", "language", string(g.language))
	g.preAction = "exec-run"
	if err := g.gdb.SendAsync(gdbCallback, "exec-run"); err != nil {
		log.Printf("Run fail, err = %s\n", err)
		// 启动失败
		return err
	}
	return nil
}

// processUserInput 循环读取用户输入
func (g *GDBDebugger) processUserInput(ctx context.Context) {
	var input string
	for {
		_, err := fmt.Scanln(&input)
		if g.statusManager.Is(Finish) {
			break
		}
		if err == nil {
			if input[len(input)-1] != '\n' {
				input = input + "\n"
			}
			g.gdb.Write([]byte(input))
		}
		time.Sleep(100 * time.Microsecond)
	}
}

// processUserOutput 循环处理用户输出
func (g *GDBDebugger) processUserOutput(ctx context.Context) {
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

func (g *GDBDebugger) StepOver() error {
	if !g.statusManager.Is(Stopped) {
		return errors.New("程序运行中，无法执行单步调试")
	}
	return g.stepOver()
}

func (g *GDBDebugger) stepOver() error {
	g.preAction = "exec-next"
	err := g.gdb.SendAsync(func(obj map[string]interface{}) {}, "exec-next")
	return err
}

func (g *GDBDebugger) StepIn() error {
	if !g.statusManager.Is(Stopped) {
		return errors.New("程序运行中，无法执行单步调试")
	}
	return g.stopIn()
}

func (g *GDBDebugger) stopIn() error {
	g.preAction = "exec-step"
	err := g.gdb.SendAsync(func(obj map[string]interface{}) {}, "exec-step")
	return err
}

func (g *GDBDebugger) StepOut() error {
	if !g.statusManager.Is(Stopped) {
		return errors.New("程序运行中，无法执行单步调试")
	}
	return g.stopOut()
}

func (g *GDBDebugger) stopOut() error {
	g.preAction = "exec-finish"
	err := g.gdb.SendAsync(func(obj map[string]interface{}) {}, "exec-finish")
	return err
}

func (g *GDBDebugger) Continue() error {
	if !g.statusManager.Is(Stopped) {
		return errors.New("程序运行中，无法执行continue")
	}
	return g.continue2()
}

func (g *GDBDebugger) continue2() error {
	g.preAction = "exec-continue"
	err := g.gdb.SendAsync(func(obj map[string]interface{}) {}, "exec-continue")
	return err
}

func (g *GDBDebugger) SetBreakpoints(source dap.Source, breakpoints []dap.SourceBreakpoint) error {
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

func (g *GDBDebugger) removeBreakpoints(numbers []string) error {
	for _, number := range numbers {
		var callback gdb2.AsyncCallback = func(m map[string]interface{}) {}
		g.gdb.SendAsync(callback, "break-delete", number)
	}
	return nil
}

func (g *GDBDebugger) Terminate() error {
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

func (g *GDBDebugger) GetStackTrace() ([]dap.StackFrame, error) {
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

func (g *GDBDebugger) GetScopes(frameId int) ([]dap.Scope, error) {
	// 读取栈帧
	return []dap.Scope{
		{Name: "Global", VariablesReference: globalScopeReference},
		{Name: "Local", VariablesReference: g.referenceUtil.GetScopesReference(frameId)},
	}, nil
}

func (g *GDBDebugger) GetVariables(reference int) ([]dap.Variable, error) {
	if !g.statusManager.Is(Stopped) {
		return nil, errors.New("程序未暂停变量信息")
	}
	var variables []dap.Variable
	var err error
	// 通过scope引用获取变量列表
	if g.referenceUtil.CheckIsGlobalScope(reference) {
		variables, err = g.getGlobalScopeVariables()
	} else if g.referenceUtil.CheckIsLocalScope(reference) {
		variables, err = g.getLocalScopeVariables(reference)
	} else {
		variables, err = g.getVariables(reference)
	}
	return variables, err
}

func (g *GDBDebugger) getVariables(reference int) ([]dap.Variable, error) {
	// 解析引用
	refStruct, err := g.referenceUtil.ParseVariableReference(reference)
	if err != nil {
		log.Printf("getVariables failed: %v\n", err)
		return nil, err
	}

	// 切换栈帧
	if err = g.selectFrame(refStruct); err != nil {
		return nil, err
	}

	// 创建变量
	targetVar := "structName"
	targetVariable, err := g.createVar(refStruct, targetVar)
	if err != nil {
		return nil, err
	}
	defer g.deleteVar(targetVar)

	// 读取变量的children元素列表
	variables, err := g.varListChildren(targetVariable, refStruct, targetVar)
	if err != nil {
		return nil, err
	}

	answer := make([]dap.Variable, 0, 10)
	for _, variable := range variables {
		// 如果value不为指针，且chidren不为0说明是结构体类型
		if !g.gdbOutputUtil.checkIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			// 已经定位了的结构体下的某个属性，直接加路径即可。
			variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(GetFieldReferenceStruct(refStruct, variable.Name))
		}
		// value指针且chidren不为0说明是指针类型
		if g.gdbOutputUtil.checkIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			if variable.Type != "char *" {
				if g.gdbOutputUtil.isShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := g.gdbOutputUtil.convertValueToAddress(variable.Value)
				variable.Value = address
				if !g.gdbOutputUtil.isNullPoint(address) {
					variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
						&ReferenceStruct{Type: "p", PointType: variable.Type, Address: address, VariableName: variable.Name})
				}
			}
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

func (g *GDBDebugger) getLocalScopeVariables(reference int) ([]dap.Variable, error) {
	frameId := g.referenceUtil.GetFrameIDByLocalReference(reference)
	var variables []dap.Variable
	var err error
	if g.functionInfos != nil {
		variables, err = g.getLocalVariables2(reference)
	} else {
		variables, err = g.getLocalVariables(reference)
	}
	if err != nil {
		return nil, err
	}
	var answer []dap.Variable
	for _, variable := range variables {
		// 结构体类型，如果value为空说明是结构体类型
		if !g.gdbOutputUtil.checkIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			// 如果parentRef不为空，说明是栈帧中的某个结构体变量
			variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
				&ReferenceStruct{Type: "v", FrameId: strconv.Itoa(frameId), VariableName: variable.Name})
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if g.gdbOutputUtil.checkIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			if variable.Type != "char *" {
				if g.gdbOutputUtil.isShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := g.gdbOutputUtil.convertValueToAddress(variable.Value)
				variable.Value = address
				if !g.gdbOutputUtil.isNullPoint(address) {
					variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
						&ReferenceStruct{Type: "p", PointType: variable.Type, Address: address, VariableName: variable.Name})
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

func (g *GDBDebugger) getGlobalScopeVariables() ([]dap.Variable, error) {
	variables, err := g.getGlobalVariables()
	if err != nil {
		return nil, err
	}
	var answer []dap.Variable
	// 遍历所有的answer
	for _, variable := range variables {
		// 结构体类型，如果value为空说明是结构体类型
		if !g.gdbOutputUtil.checkIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			// 如果parentRef不为空，说明是栈帧中的某个结构体变量
			variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
				&ReferenceStruct{Type: "v", FrameId: "0", VariableName: variable.Name})
			variable.Value = ""
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if g.gdbOutputUtil.checkIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			if variable.Type != "char *" {
				if g.gdbOutputUtil.isShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := g.gdbOutputUtil.convertValueToAddress(variable.Value)
				variable.Value = address
				if !g.gdbOutputUtil.isNullPoint(address) {
					variable.VariablesReference, _ = g.referenceUtil.CreateVariableReference(
						&ReferenceStruct{Type: "p", PointType: variable.Type, Address: address, VariableName: variable.Name})
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

// getGlobalVariables 获取全局变量列表
func (g *GDBDebugger) getGlobalVariables() ([]dap.Variable, error) {
	m, err := g.sendWithTimeOut(OptionTimeout, "symbol-info-variables", "--max-results", "40")
	if err != nil {
		return nil, err
	}
	variables := g.gdbOutputUtil.parseGlobalVariableOutput(g.gdb, m)
	return variables, nil
}

// getLocalVariables 获取本地变量列表1，通过gdb命令获取，会存在有些未初始化变量却被使用情况
func (g *GDBDebugger) getLocalVariables(reference int) ([]dap.Variable, error) {
	frameId := g.referenceUtil.GetFrameIDByLocalReference(reference)
	// 获取当前线程id
	currentThreadId, _ := g.getCurrentThreadId()
	// 获取栈帧中所有局部变量
	m, err := g.sendWithTimeOut(OptionTimeout, "stack-list-variables",
		"--thread", currentThreadId, "--frame", strconv.Itoa(frameId), "--skip-unavailable", "2")
	if err != nil {
		log.Printf("getLocalScopeVariables failed: %v\n", err)
		return nil, err
	}
	variables := g.gdbOutputUtil.parseFrameVariablesOutput(g.gdb, m)
	return variables, nil
}

// getLocalVariables2 通过静态代码分析获取
func (g *GDBDebugger) getLocalVariables2(reference int) ([]dap.Variable, error) {
	frameId := g.referenceUtil.GetFrameIDByLocalReference(reference)
	stackTrace, err := g.GetStackTrace()
	if err != nil {
		return g.getLocalVariables(reference)
	}
	// 找到目标栈帧id
	var targetFrame dap.StackFrame
	for _, f := range stackTrace {
		if f.Id == frameId {
			targetFrame = f
			break
		}
	}
	// 获取当前方法
	var target *FunctionInfo
	for _, f := range g.functionInfos {
		if f.Name == targetFrame.Name {
			target = &f
			break
		}
	}
	if target == nil {
		return g.getLocalVariables(reference)
	}

	// 获取目标变量列表
	targetVariableNames := []string{}
	for _, v := range target.Variables {
		if v.Location.Line < targetFrame.Line {
			targetVariableNames = append(targetVariableNames, v.Name)
		}
	}

	// 读取变量列表
	var answer []dap.Variable
	for _, variableName := range targetVariableNames {
		m2, err := g.gdb.SendWithTimeout(OptionTimeout, "var-create", variableName, "*", variableName)
		if err != nil {
			logrus.Errorf("getChidrenNumber fail err = %s", err)
			continue
		}
		variable := g.gdbOutputUtil.parseVarCreate(m2)
		if variable == nil {
			continue
		}
		answer = append(answer, *variable)
		_, _ = g.gdb.SendWithTimeout(OptionTimeout, "var-delete", variableName)
	}
	return answer, nil
}

// selectFrame 如果是普通类型需要切换栈帧，同一个变量名，可能在不同栈帧中会有重复，需要定位栈帧和变量名称才能读取到变量值
func (g *GDBDebugger) selectFrame(ref *ReferenceStruct) error {
	if ref.Type == StructType {
		if _, err := g.sendWithTimeOut(OptionTimeout, "stack-select-frame", ref.FrameId); err != nil {
			return err
		}
	}
	return nil
}

// createVar 创建变量，在读取一个值的时候，需要创建变量以后才能读取
func (g *GDBDebugger) createVar(ref *ReferenceStruct, structName string) (*dap.Variable, error) {
	_, _ = g.gdb.SendWithTimeout(OptionTimeout, "enable-pretty-printing")
	exp := g.getExport(ref)
	m, err := g.sendWithTimeOut(OptionTimeout, "var-create", structName, "*", exp)
	if err != nil {
		logrus.Errorf("create var fail %s", err)
		return nil, err
	}
	variable := g.gdbOutputUtil.parseVarCreate(m)
	return variable, nil
}

// getExport 通过ReferenceStruct，获取变量表达式
func (g *GDBDebugger) getExport(ref *ReferenceStruct) string {
	var exp string
	if ref.Type == "v" {
		exp = ref.VariableName
	} else if ref.Type == "p" {
		exp = fmt.Sprintf("(%s)%s", ref.PointType, ref.Address)
	}
	if ref.FieldPath != "" {
		exp = fmt.Sprintf("(%s).%s", exp, ref.FieldPath)
	}
	return exp
}

// deleteVar 删除变量，创建完变量需要进行删除，避免再次创建时名称重复
func (g *GDBDebugger) deleteVar(name string) error {
	_, err := g.sendWithTimeOut(OptionTimeout, "var-delete", name)
	return err
}

// varListChildrenForCpp c++中var-list-children会因为一些private、public修饰符而无法获取结构体内容，需要特殊处理
func (g *GDBDebugger) varListChildrenForCpp(ref *ReferenceStruct, targetVariable *dap.Variable) ([]dap.Variable, error) {
	if !g.checkIsCppArrayType(targetVariable) {
		return g.varListChildrenForCppStruct(ref)
	} else {
		return g.varListChildrenForCppArray(ref, targetVariable)
	}
}

func (g *GDBDebugger) varListChildrenForCppStruct(ref *ReferenceStruct) ([]dap.Variable, error) {
	// 读取结构体值
	exp := g.getExport(ref)
	_, _ = g.gdb.SendWithTimeout(OptionTimeout, "enable-pretty-printing")
	m, err := g.sendWithTimeOut(OptionTimeout, "data-evaluate-expression", exp)
	if err != nil {
		log.Printf("varListChildren fail, err = %s\n", err)
		return nil, err
	}
	payload := g.gdbOutputUtil.getInterfaceFromMap(m, "payload")
	value := g.gdbOutputUtil.getStringFromMap(payload, "value")
	keys := g.parseObject2Keys(value)

	answer := []dap.Variable{}
	for _, key := range keys {
		m, err = g.sendWithTimeOut(OptionTimeout, "var-create", key, "*", fmt.Sprintf("(%s).%s", exp, key))
		if err != nil {
			log.Printf("varListChildren fail, err = %s\n", err)
			continue
		}
		variable := g.gdbOutputUtil.parseVarCreate(m)
		if variable != nil {
			answer = append(answer, *variable)
		}
		_, _ = g.sendWithTimeOut(OptionTimeout, "var-delete", key)
	}
	return answer, nil
}

func (g *GDBDebugger) varListChildrenForCppArray(ref *ReferenceStruct, targetVariable *dap.Variable) ([]dap.Variable, error) {
	var arrayLenth int
	// 模式1: 匹配std::array<T, N>格式
	stdArrayPattern := `std::array<[^,]+,\s*(\d+)\s*>`
	stdArrayRegex := regexp.MustCompile(stdArrayPattern)
	if match := stdArrayRegex.FindStringSubmatch(targetVariable.Type); match != nil {
		arrayLenth, _ = strconv.Atoi(match[1])
	}
	if arrayLenth == 0 {
		// 模式2: 匹配arr[N]格式
		cArrayPattern := `\w+\s*\[\s*(\d+)\s*\]`
		cArrayRegex := regexp.MustCompile(cArrayPattern)
		if match := cArrayRegex.FindStringSubmatch(targetVariable.Type); match != nil {
			arrayLenth, _ = strconv.Atoi(match[1])
		}
	}

	answer := []dap.Variable{}
	exp := g.getExport(ref)
	for i := 0; i < arrayLenth; i++ {
		m, err := g.sendWithTimeOut(OptionTimeout, "var-create", "arrayStruct", "*", fmt.Sprintf("%s.%s", exp, i))
		if err != nil {
			log.Printf("varListChildren fail, err = %s\n", err)
			continue
		}
		variable := g.gdbOutputUtil.parseVarCreate(m)
		if variable != nil {
			answer = append(answer, *variable)
		}
		_, _ = g.sendWithTimeOut(OptionTimeout, "var-delete", "arrayStruct")
	}
	return answer, nil
}

func (g *GDBDebugger) checkIsCppArrayType(targetVariable *dap.Variable) bool {
	// 校验std::array<int, N>格式
	stdArrayPattern := `std::array<[^,]+,\s*(\d+)\s*>`
	stdArrayRegex := regexp.MustCompile(stdArrayPattern)

	// 校验arr[N]格式（变量名+方括号数字）
	cArrayPattern := `\w+\s*\[\s*(\d+)\s*\]`
	cArrayRegex := regexp.MustCompile(cArrayPattern)

	return stdArrayRegex.MatchString(targetVariable.Type) || cArrayRegex.MatchString(targetVariable.Type)
}

func (g *GDBDebugger) parseObject2Keys(inputStr string) []string {
	// 定义正则表达式模式，匹配 = 前面的键
	re := regexp.MustCompile(`(\w+)\s*=`)
	// 查找所有匹配项
	matches := re.FindAllStringSubmatch(inputStr, -1)
	answer := []string{}
	for _, match := range matches {
		key := match[1]
		if key != "\000" {
			answer = append(answer, key)
		}
	}
	return answer
}

// varListChildren 读取变量的children元素列表 ）
func (g *GDBDebugger) varListChildren(targetVariable *dap.Variable, ref *ReferenceStruct, structName string) ([]dap.Variable, error) {
	if g.language == constants.LanguageCpp {
		// 如果是结构体类型，而且是c++语言，那么需要周varListChildren2
		return g.varListChildrenForCpp(ref, targetVariable)
	}
	// 获取所有children列表并解析
	var m map[string]interface{}
	var err error
	m, err = g.sendWithTimeOut(OptionTimeout, "var-list-children", "1", structName)
	if err != nil {
		log.Printf("getVariables fail, err = %s\n", err)
		return nil, err
	}
	variables := g.gdbOutputUtil.parseVariablesOutput(m)
	return variables, nil
}

func (g *GDBDebugger) checkAndSetArrayAddress(variable dap.Variable) (string, error) {
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
func (g *GDBDebugger) getCurrentThreadId() (string, error) {
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
func (g *GDBDebugger) gdbNotificationCallback(m map[string]interface{}) {
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
func (g *GDBDebugger) processStoppedData(m interface{}) {
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
func (g *GDBDebugger) processRunningData() {
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
func (g *GDBDebugger) getChildrenNumber(name string) int {
	m, err := g.sendWithTimeOut(OptionTimeout, "var-create", name, "*", name)
	if err != nil {
		logrus.Errorf("getChidrenNumber fail err = %s", err)
		return 0
	}
	defer func() {
		_, _ = g.sendWithTimeOut(OptionTimeout, "var-delete", name)
	}()
	v := g.gdbOutputUtil.parseVarCreate(m)
	if v != nil {
		return v.IndexedVariables
	}
	return 0
}

func (g *GDBDebugger) sendWithTimeOut(timeout time.Duration, operation string, args ...string) (map[string]interface{}, error) {
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
