package cpp_debugger

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fansqz/go-debugger/constants"
	. "github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/debugger/gdb_debugger"
	"github.com/fansqz/go-debugger/debugger/gdb_debugger/gdb"
	"github.com/fansqz/go-debugger/debugger/utils"
	"github.com/google/go-dap"
)

const (
	OptionTimeout = time.Second * 10
)

type CPPDebugger struct {
	// 因为都是gdb调试器，所以使用c调试器即可
	gdbDebugger   *gdb_debugger.GDBDebugger
	gdbOutputUtil *gdb_debugger.GDBOutputUtil
	statusManager *utils.StatusManager
	referenceUtil *gdb_debugger.ReferenceUtil
	gdb           *gdb.Gdb
}

func NewCPPDebugger() *CPPDebugger {
	gdbDebugger := gdb_debugger.NewGDBDebugger(constants.LanguageCpp)
	d := &CPPDebugger{
		gdbDebugger:   gdbDebugger,
		gdbOutputUtil: gdbDebugger.GdbOutputUtil,
		statusManager: gdbDebugger.StatusManager,
		referenceUtil: gdbDebugger.ReferenceUtil,
	}
	return d
}

func (c *CPPDebugger) Start(option *StartOption) error {
	err := c.gdbDebugger.Start(option)
	c.gdb = c.gdbDebugger.GDB
	return err
}

// Run 同步方法，开始运行
func (c *CPPDebugger) Run() error {
	return c.gdbDebugger.Run()
}

func (c *CPPDebugger) StepOver() error {
	return c.gdbDebugger.StepOver()
}

func (c *CPPDebugger) StepIn() error {
	return c.gdbDebugger.StepIn()
}

func (c *CPPDebugger) StepOut() error {
	return c.gdbDebugger.StepOut()
}

func (c *CPPDebugger) Continue() error {
	return c.gdbDebugger.Continue()
}

func (c *CPPDebugger) SetBreakpoints(source dap.Source, breakpoints []dap.SourceBreakpoint) error {
	return c.gdbDebugger.SetBreakpoints(source, breakpoints)
}

func (c *CPPDebugger) GetStackTrace() ([]dap.StackFrame, error) {
	return c.gdbDebugger.GetStackTrace()
}

func (c *CPPDebugger) GetScopes(frameId int) ([]dap.Scope, error) {
	return c.gdbDebugger.GetScopes(frameId)
}

func (c *CPPDebugger) Terminate() error {
	return c.gdbDebugger.Terminate()
}

// GetVariables cpp的获取变量列表的时候需要进行特殊处理
// 因为列表可能存在public，private等修饰符
func (c *CPPDebugger) GetVariables(reference int) ([]dap.Variable, error) {
	if !c.statusManager.Is(utils.Stopped) {
		return nil, errors.New("程序未暂停变量信息")
	}
	var variables []dap.Variable
	var err error
	// 通过scope引用获取变量列表
	if c.referenceUtil.CheckIsGlobalScope(reference) {
		variables, err = c.gdbDebugger.GetGlobalScopeVariables()
	} else if c.referenceUtil.CheckIsLocalScope(reference) {
		variables, err = c.gdbDebugger.GetLocalScopeVariables(reference)
	} else {
		variables, err = c.getVariables(reference)
	}
	return variables, err
}

// CompileCPPFile 开始编译文件
func CompileCPPFile(workPath string, code string) (string, error) {
	// 创建工作目录, 用户的临时文件
	if err := os.MkdirAll(workPath, os.ModePerm); err != nil {
		return "", err
	}

	// 保存待编译文件
	codeFile := path.Join(workPath, "main.cpp")
	err := os.WriteFile(codeFile, []byte(code), 777)
	if err != nil {
		return "", err
	}
	execFile := path.Join(workPath, "main")

	cmd := exec.Command("g++", "-g", "-O0",
		"-fno-inline-functions",
		"-ftrivial-auto-var-init=zero", "-fsanitize=undefined", "-fno-omit-frame-pointer",
		"-fno-reorder-blocks-and-partition", "-fvar-tracking-assignments", codeFile, "-o", execFile)
	_, err = cmd.Output()
	if err != nil {
		return "", err
	}
	return execFile, err
}

func (c *CPPDebugger) getVariables(reference int) ([]dap.Variable, error) {
	// 解析引用
	refStruct, err := c.referenceUtil.ParseVariableReference(reference)
	if err != nil {
		log.Printf("getVariables failed: %v\n", err)
		return nil, err
	}

	// 切换栈帧
	if err = c.gdbDebugger.SelectFrame(refStruct); err != nil {
		return nil, err
	}

	// 创建变量
	targetVar := "structName"
	targetVariable, err := c.gdbDebugger.CreateVar(refStruct, targetVar)
	if err != nil {
		return nil, err
	}
	defer c.gdbDebugger.DeleteVar(targetVar)

	// 读取变量的children元素列表
	variables, err := c.varListChildrenForCpp(refStruct, targetVariable)
	if err != nil {
		return nil, err
	}

	answer := make([]dap.Variable, 0, 10)
	for _, variable := range variables {
		// 如果value不为指针，且chidren不为0说明是结构体类型
		if !c.gdbOutputUtil.CheckIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			// 已经定位了的结构体下的某个属性，直接加路径即可。
			variable.VariablesReference, _ = c.referenceUtil.CreateVariableReference(gdb_debugger.GetFieldReferenceStruct(refStruct, variable.Name))
		}
		// value指针且chidren不为0说明是指针类型
		if c.gdbOutputUtil.CheckIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			if variable.Type != "char *" {
				if c.gdbOutputUtil.IsShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := c.gdbOutputUtil.ConvertValueToAddress(variable.Value)
				variable.Value = address
				if !c.gdbOutputUtil.IsNullPoint(address) {
					variable.VariablesReference, _ = c.referenceUtil.CreateVariableReference(
						&gdb_debugger.ReferenceStruct{Type: "p", PointType: variable.Type, Address: address, VariableName: variable.Name})
				}
			}
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

// varListChildrenForCpp c++中var-list-children会因为一些private、public修饰符而无法获取结构体内容，需要特殊处理
func (c *CPPDebugger) varListChildrenForCpp(ref *gdb_debugger.ReferenceStruct, targetVariable *dap.Variable) ([]dap.Variable, error) {
	if !c.checkIsCppArrayType(targetVariable) {
		return c.varListChildrenForCppStruct(ref)
	} else {
		return c.varListChildrenForCppArray(ref, targetVariable)
	}
}

func (c *CPPDebugger) varListChildrenForCppStruct(ref *gdb_debugger.ReferenceStruct) ([]dap.Variable, error) {
	// 读取结构体值
	exp := c.GetExport(ref)
	_, _ = c.gdbDebugger.GDB.SendWithTimeout(OptionTimeout, "enable-pretty-printing")
	m, err := c.gdbDebugger.GDB.SendWithTimeout(OptionTimeout, "data-evaluate-expression", exp)
	if err != nil {
		log.Printf("varListChildren fail, err = %s\n", err)
		return nil, err
	}
	payload := c.gdbOutputUtil.GetInterfaceFromMap(m, "payload")
	value := c.gdbOutputUtil.GetStringFromMap(payload, "value")
	keys := c.parseObject2Keys(value)

	answer := []dap.Variable{}
	for _, key := range keys {
		m, err = c.gdb.SendWithTimeout(OptionTimeout, "var-create", key, "*", fmt.Sprintf("(%s).%s", exp, key))
		if err != nil {
			log.Printf("varListChildren fail, err = %s\n", err)
			continue
		}
		variable := c.gdbOutputUtil.ParseVarCreate(m)
		if variable != nil {
			answer = append(answer, *variable)
		}
		_, _ = c.gdb.SendWithTimeout(OptionTimeout, "var-delete", key)
	}
	return answer, nil
}

func (c *CPPDebugger) varListChildrenForCppArray(ref *gdb_debugger.ReferenceStruct, targetVariable *dap.Variable) ([]dap.Variable, error) {
	var arrayLength int

	// 模式1: 匹配std::array<T, N>格式，同时捕获类型T和长度N
	stdArrayPattern := `std::array<([^,]+),\s*(\d+)\s*>`
	stdArrayRegex := regexp.MustCompile(stdArrayPattern)
	if match := stdArrayRegex.FindStringSubmatch(targetVariable.Type); match != nil {
		arrayLength, _ = strconv.Atoi(match[2])
	}

	if arrayLength == 0 {
		// 模式2: 匹配arr[N]格式，尝试从父类型推断元素类型
		cArrayPattern := `(\w+)\s*\[\s*(\d+)\s*\]`
		cArrayRegex := regexp.MustCompile(cArrayPattern)
		if match := cArrayRegex.FindStringSubmatch(targetVariable.Type); match != nil {
			// 尝试从父类型推断元素类型（可能不准确，需要根据实际情况调整）
			arrayLength, _ = strconv.Atoi(match[2])
		}
	}
	exp := c.gdbDebugger.GetExport(ref)
	if arrayLength == 0 {
		// 模式3：匹配std::vector<int, std::allocator<int> > 通过size()获取数组长度
		if strings.Contains(targetVariable.Type, "std::vector") {
			m, err := c.gdb.SendWithTimeout(OptionTimeout, "data-evaluate-expression", fmt.Sprintf("%s.size()", exp))
			if err != nil {
				log.Printf("varListChildrenForCppArray data-evaluate-expression fail, err = %s\n", err)
			} else {
				payload := c.gdbOutputUtil.GetInterfaceFromMap(m, "payload")
				value := c.gdbOutputUtil.GetStringFromMap(payload, "value")
				arrayLength, _ = strconv.Atoi(value)
			}
			// 兜底，避免一些函数内联导致size不可用
			if arrayLength == 0 {
				var typ string
				re := regexp.MustCompile(`\bstd::\w+<\s*([^,\s>]+)(?:\s*,|\s*>)`)
				if match := re.FindStringSubmatch(targetVariable.Type); match != nil {
					typ = strings.TrimSpace(match[1])
				}
				if typ != "" {
					m, _ = c.gdb.SendWithTimeout(OptionTimeout, "data-evaluate-expression", fmt.Sprintf("sizeof(%s)/sizeof(%s)", exp, typ))
					payload := c.gdbOutputUtil.GetInterfaceFromMap(m, "payload")
					value := c.gdbOutputUtil.GetStringFromMap(payload, "value")
					arrayLength, _ = strconv.Atoi(value)
				}
			}

		}
	}

	answer := []dap.Variable{}
	for i := 0; i < arrayLength; i++ {
		m, err := c.gdb.SendWithTimeout(OptionTimeout, "var-create", "arrayNameChildren", "*", fmt.Sprintf("%s[%d]", exp, i))
		if err != nil {
			log.Printf("varListChildren fail, err = %s\n", err)
			continue
		}
		variable := c.gdbOutputUtil.ParseVarCreate(m)
		if variable != nil {
			variable.Name = strconv.Itoa(i)
			// 设置元素类型信息
			answer = append(answer, *variable)
		}
		_, _ = c.gdb.SendWithTimeout(OptionTimeout, "var-delete", "arrayNameChildren")
	}
	return answer, nil
}

func (g *CPPDebugger) checkIsCppArrayType(targetVariable *dap.Variable) bool {
	// 校验std::array<int, N>格式
	stdArrayPattern := `std::array<[^,]+,\s*(\d+)\s*>`
	stdArrayRegex := regexp.MustCompile(stdArrayPattern)

	// 校验arr[N]格式（变量名+方括号数字）
	cArrayPattern := `\w+\s*\[\s*(\d+)\s*\]`
	cArrayRegex := regexp.MustCompile(cArrayPattern)

	return stdArrayRegex.MatchString(targetVariable.Type) || cArrayRegex.MatchString(targetVariable.Type) ||
		strings.Contains(targetVariable.Type, "std::vector")
}

func (c *CPPDebugger) parseObject2Keys(inputStr string) []string {
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

// getExport 通过ReferenceStruct，获取变量表达式
func (c *CPPDebugger) GetExport(ref *gdb_debugger.ReferenceStruct) string {
	var exp string
	if ref.Type == "v" {
		exp = ref.VariableName
	} else if ref.Type == "p" {
		exp = fmt.Sprintf("*(%s)%s", ref.PointType, ref.Address)
	}
	if ref.FieldPath != "" {
		exp = fmt.Sprintf("(%s).%s", exp, ref.FieldPath)
	}
	return exp
}
