package c_debugger

import (
	"github.com/fansqz/go-debugger/constants"
	. "github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/debugger/gdb_debugger"
	"github.com/google/go-dap"
	"time"
)

const (
	OptionTimeout = time.Second * 10
)

type CDebugger struct {
	// 因为都是gdb调试器，所以使用c调试器即可
	gdbDebugger *gdb_debugger.GDBDebugger
}

func NewCDebugger() *CDebugger {
	d := &CDebugger{
		gdbDebugger: gdb_debugger.NewGDBDebugger(constants.LanguageC),
	}
	return d
}

func (c *CDebugger) Start(option *StartOption) error {
	return c.gdbDebugger.Start(option)
}

// Run 同步方法，开始运行
func (c *CDebugger) Run() error {
	return c.gdbDebugger.Run()
}

func (c *CDebugger) StepOver() error {
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

func (c *CDebugger) GetVariables(reference int) ([]dap.Variable, error) {
	return c.gdbDebugger.GetVariables(reference)
}

func (c *CDebugger) Terminate() error {
	return c.gdbDebugger.Terminate()
}
