package debugger

import (
	"context"
)

type NotificationCallback func(interface{})

// Debugger
// 用户的一次调试过程处理
// debugger目前设置为支持多文件的
// 需要保证并发安全
type Debugger interface {
	// Start
	// 开始调试，及调用start命令，callback用来异步处理用户程序输出
	Start(ctx context.Context, option *StartOption) error
	// Send 输入
	Send(ctx context.Context, input string) error
	// StepOver 下一步，不会进入函数内部
	StepOver(ctx context.Context) error
	// StepIn 下一步，会进入函数内部
	StepIn(ctx context.Context) error
	// StepOut 单步退出
	StepOut(ctx context.Context) error
	// Continue 忽略继续执行
	Continue(ctx context.Context) error
	// AddBreakpoints 添加断点
	// 返回的是添加成功的断点
	AddBreakpoints(ctx context.Context, breakpoints []*Breakpoint) error
	// RemoveBreakpoints 移除断点
	// 返回的是移除成功的断点
	RemoveBreakpoints(ctx context.Context, breakpoints []*Breakpoint) error
	// GetStackTrace 获取栈帧
	GetStackTrace(ctx context.Context) ([]*StackFrame, error)
	// GetFrameVariables 获取某个栈帧中的变量列表
	GetFrameVariables(ctx context.Context, frameId string) ([]*Variable, error)
	// GetVariables 查看引用的值
	GetVariables(ctx context.Context, reference string) ([]*Variable, error)
	// Terminate 终止调试
	// 调用完该命令以后可以重新Launch
	Terminate(ctx context.Context) error
	// StructVisual 以结构体为导向的可视化方法，一般用作树、图的可视化
	StructVisual(ctx context.Context, query *StructVisualQuery) (*StructVisualData, error)
	// VariableVisual 以变量导向的可视化方法，一般用作数组的可视化
	// 传递的是数组的变量名称、作为数组指针的变量名称等
	VariableVisual(ctx context.Context, query *VariableVisualQuery) (*VariableVisualData, error)
}
