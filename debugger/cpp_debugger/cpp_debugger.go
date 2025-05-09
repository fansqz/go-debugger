package cpp_debugger

import (
	"github.com/fansqz/go-debugger/constants"
	. "github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/debugger/gdb_debugger"
	"github.com/google/go-dap"
)

type CPPDebugger struct {
	// 因为都是gdb调试器，所以使用c调试器即可
	gdbDebugger *gdb_debugger.GDBDebugger
}

func NewCPPDebugger() *CPPDebugger {
	d := &CPPDebugger{
		gdbDebugger: gdb_debugger.NewGDBDebugger(constants.LanguageCpp),
	}
	return d
}

func (c *CPPDebugger) Start(option *StartOption) error {
	return c.gdbDebugger.Start(option)
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

// GetVariables cpp的获取变量列表的时候需要进行特殊处理
// 因为列表可能存在public，private等修饰符
func (c *CPPDebugger) GetVariables(reference int) ([]dap.Variable, error) {
	return c.gdbDebugger.GetVariables(reference)
}

func (c *CPPDebugger) Terminate() error {
	return c.gdbDebugger.Terminate()
}
