package c_debugger

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"time"

	"github.com/fansqz/go-debugger/constants"
	. "github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/debugger/gdb_debugger"
	"github.com/fansqz/go-debugger/debugger/gdb_debugger/gdb"
	"github.com/fansqz/go-debugger/debugger/utils"
	"github.com/google/go-dap"
	"github.com/smacker/go-tree-sitter/javascript"
)

const (
	OptionTimeout = time.Second * 10
)

type CDebugger struct {
	// 因为都是gdb调试器，所以使用c调试器即可
	gdbDebugger   *gdb_debugger.GDBDebugger
	gdbOutputUtil *gdb_debugger.GDBOutputUtil
	statusManager *utils.StatusManager
	referenceUtil *gdb_debugger.ReferenceUtil
	gdb           *gdb.Gdb
}

func NewCDebugger() *CDebugger {
	gdbDebugger := gdb_debugger.NewGDBDebugger(constants.LanguageC)
	d := &CDebugger{
		gdbDebugger:   gdbDebugger,
		gdbOutputUtil: gdbDebugger.GdbOutputUtil,
		statusManager: gdbDebugger.StatusManager,
		referenceUtil: gdbDebugger.ReferenceUtil,
	}
	return d
}

func (c *CDebugger) Start(option *StartOption) error {
	err := c.gdbDebugger.Start(option)
	c.gdb = c.gdbDebugger.GDB
	return err
}

// Run 同步方法，开始运行
func (c *CDebugger) Run() error {
	return c.gdbDebugger.Run()
}

func (c *CDebugger) StepOver() error {
	javascript.GetLanguage()
	return c.gdbDebugger.StepOver()
}

func (c *CDebugger) StepIn() error {
	return c.gdbDebugger.StepIn()
}

func (c *CDebugger) StepOut() error {
	return c.gdbDebugger.StepOut()
}

func (c *CDebugger) Continue() error {
	return c.gdbDebugger.Continue()
}

func (c *CDebugger) SetBreakpoints(source dap.Source, breakpoints []dap.SourceBreakpoint) error {
	return c.gdbDebugger.SetBreakpoints(source, breakpoints)
}

func (c *CDebugger) GetStackTrace() ([]dap.StackFrame, error) {
	return c.gdbDebugger.GetStackTrace()
}

func (c *CDebugger) GetScopes(frameId int) ([]dap.Scope, error) {
	return c.gdbDebugger.GetScopes(frameId)
}

func (c *CDebugger) Terminate() error {
	return c.gdbDebugger.Terminate()
}

func (c *CDebugger) GetVariables(reference int) ([]dap.Variable, error) {
	if !c.gdbDebugger.StatusManager.Is(utils.Stopped) {
		return nil, errors.New("程序未暂停变量信息")
	}
	var variables []dap.Variable
	var err error
	// 通过scope引用获取变量列表
	if c.gdbDebugger.ReferenceUtil.CheckIsGlobalScope(reference) {
		variables, err = c.getGlobalScopeVariables()
	} else if c.gdbDebugger.ReferenceUtil.CheckIsLocalScope(reference) {
		variables, err = c.getLocalScopeVariables(reference)
	} else {
		variables, err = c.getVariables(reference)
	}
	return variables, err
}

func (c *CDebugger) getLocalScopeVariables(reference int) ([]dap.Variable, error) {
	variables, err := c.gdbDebugger.GetLocalScopeVariables(reference)
	if err != nil {
		return nil, err
	}
	frameId := c.referenceUtil.GetFrameIDByLocalReference(reference)
	var answer []dap.Variable
	for _, variable := range variables {
		// 结构体类型，如果value为空说明是结构体类型
		if !c.gdbOutputUtil.CheckIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			// 如果parentRef不为空，说明是栈帧中的某个结构体变量
			variable.VariablesReference, _ = c.referenceUtil.CreateVariableReference(
				&gdb_debugger.ReferenceStruct{Type: "v", FrameId: strconv.Itoa(frameId), VariableName: variable.Name, VariableType: variable.Type})
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if c.gdbOutputUtil.CheckIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			if variable.Type != "char *" {
				if c.gdbOutputUtil.IsShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := c.gdbOutputUtil.ConvertValueToAddress(variable.Value)
				variable.Value = address
				if !c.gdbOutputUtil.IsNullPoint(address) {
					variable.VariablesReference, _ = c.referenceUtil.CreateVariableReference(
						&gdb_debugger.ReferenceStruct{Type: "p", VariableType: variable.Type, Address: address, VariableName: variable.Name})
				}
			}
		}
		// 如果是数组类型，设置value为数组的首地址
		addr, err := c.checkAndSetArrayAddress(variable)
		if err != nil {
			log.Printf("checkAndSetArrayAddress failed: %v\n", err)
		} else if addr != "" {
			variable.Value = addr
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

func (c *CDebugger) getGlobalScopeVariables() ([]dap.Variable, error) {
	variables, err := c.gdbDebugger.GetGlobalVariables()
	if err != nil {
		return nil, err
	}
	var answer []dap.Variable
	// 遍历所有的answer
	for _, variable := range variables {
		// 结构体类型，如果value为空说明是结构体类型
		if !c.gdbOutputUtil.CheckIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			// 如果parentRef不为空，说明是栈帧中的某个结构体变量
			variable.VariablesReference, _ = c.referenceUtil.CreateVariableReference(
				&gdb_debugger.ReferenceStruct{Type: "v", FrameId: "0", VariableName: variable.Name, VariableType: variable.Type})
			variable.Value = ""
		}
		// 指针类型，如果有值，但是children又不为0说明是指针类型
		if c.gdbOutputUtil.CheckIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			if variable.Type != "char *" {
				if c.gdbOutputUtil.IsShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := c.gdbOutputUtil.ConvertValueToAddress(variable.Value)
				variable.Value = address
				if !c.gdbOutputUtil.IsNullPoint(address) {
					variable.VariablesReference, _ = c.referenceUtil.CreateVariableReference(
						&gdb_debugger.ReferenceStruct{Type: "p", VariableType: variable.Type, Address: address, VariableName: variable.Name})
				}
			}
		}
		// 如果是数组类型，设置value为数组的首地址
		addr, err := c.checkAndSetArrayAddress(variable)
		if err != nil {
			log.Printf("checkAndSetArrayAddress failed: %v\n", err)
		} else if addr != "" {
			variable.Value = addr
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

func (c *CDebugger) checkAndSetArrayAddress(variable dap.Variable) (string, error) {
	pattern := `\w+\s*\[\d*\]`
	re, err := regexp.Compile(pattern)
	if err != nil {
		log.Printf("checkAndSetArrayAddress fail, err = %s\n", err)
		return "", err
	}
	if re.MatchString(variable.Type) {
		// 如果是类型是数组类型，需要设置value为地址，用于数组可视化
		m, err := c.gdb.SendWithTimeout(OptionTimeout, "data-evaluate-expression", "&"+variable.Name)
		if err != nil {
			log.Printf("checkAndSetArrayAddress fail, err = %s\n", err)
			return "", err
		}
		payload := c.gdbOutputUtil.GetInterfaceFromMap(m, "payload")
		return c.gdbOutputUtil.GetStringFromMap(payload, "value"), nil
	}
	return "", nil
}

func (c *CDebugger) getVariables(reference int) ([]dap.Variable, error) {
	variables, err := c.gdbDebugger.GetVariables(reference)
	if err != nil {
		return nil, err
	}
	refStruct, err := c.referenceUtil.ParseVariableReference(reference)
	// 解析c语言结构体，并二次处理
	answer := make([]dap.Variable, 0, 10)
	for _, variable := range variables {
		// 如果value不为指针，且chidren不为0说明是结构体类型
		if !c.gdbOutputUtil.CheckIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			// 已经定位了的结构体下的某个属性，直接加路径即可。
			variable.VariablesReference, _ = c.referenceUtil.CreateVariableReference(gdb_debugger.GetFieldReferenceStruct(refStruct, variable.Name))
		}
		// value指针且chidren不为0说明是指针类型
		if c.gdbDebugger.GdbOutputUtil.CheckIsAddress(variable.Value) && variable.IndexedVariables != 0 {
			if variable.Type != "char *" {
				if c.gdbOutputUtil.IsShouldBeFilterAddress(variable.Value) {
					continue
				}
				address := c.gdbOutputUtil.ConvertValueToAddress(variable.Value)
				variable.Value = address
				if !c.gdbOutputUtil.IsNullPoint(address) {
					variable.VariablesReference, _ = c.referenceUtil.CreateVariableReference(
						&gdb_debugger.ReferenceStruct{Type: "p", VariableType: variable.Type, Address: address, VariableName: variable.Name})
				}
			}
		}
		answer = append(answer, variable)
	}
	return answer, nil
}

// CompileCFile 开始编译文件
func CompileCFile(workPath string, code string) (string, error) {
	// 创建工作目录, 用户的临时文件
	if err := os.MkdirAll(workPath, os.ModePerm); err != nil {
		return "", err
	}

	// 保存待编译文件
	codeFile := path.Join(workPath, "main.c")
	err := os.WriteFile(codeFile, []byte(code), 777)
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
