package protocol

import "go-debugger/constants"

// StartDebugRequest 启动调试请求
type StartDebugRequest struct {
	Type     constants.RequestType `json:"type"`
	Sequence uint                  `json:"sequence"`
	// 请求序列号
	// 用户代码
	Code string `json:"code"`
	// Language 调试语言
	Language constants.LanguageType `json:"language"`
	// 初始断点
	Breakpoints []int `json:"breakpoints"`
}

// AddBreakpointRequest 增加断点请求
type AddBreakpointRequest struct {
	Type constants.RequestType `json:"type"`
	// 请求序列号
	Sequence    uint  `json:"sequence"`
	Breakpoints []int `json:"breakpoints"`
}

// RemoveBreakpointRequest 移除断点请求
type RemoveBreakpointRequest struct {
	Type constants.RequestType `json:"type"`
	// 请求序列号
	Sequence    uint  `json:"sequence"`
	Breakpoints []int `json:"breakpoints"`
}

// SendToConsoleRequest 输入到控制台
type SendToConsoleRequest struct {
	Type constants.RequestType `json:"type"`
	// 请求序列号
	Sequence uint   `json:"sequence"`
	Content  string `json:"content"`
}

// StepRequest next
type StepRequest struct {
	Type constants.RequestType `json:"type"`
	// 请求序列号
	Sequence uint               `json:"sequence"`
	StepType constants.StepType `json:"stepType"`
}

// ContinueRequest continue
type ContinueRequest struct {
	Type constants.RequestType `json:"type"`
	// 请求序列号
	Sequence uint `json:"sequence"`
}

// TerminateRequest 关闭调试
type TerminateRequest struct {
	Type constants.RequestType `json:"type"`
	// 请求序列号
	Sequence uint `json:"sequence"`
}

// GetFrameVariables 根据栈帧获取变量列表
type GetFrameVariables struct {
	Type    constants.RequestType `json:"type"`
	FrameID string                `json:"frameID"`
	// 请求序列号
	Sequence uint `json:"sequence"`
}

// GetStackTrace 获取栈列表
type GetStackTrace struct {
	Type constants.RequestType `json:"type"`
	// 请求序列号
	Sequence uint `json:"sequence"`
}

// GetVariablesRequest 根据引用获取变量信息，如果是指针，获取指针指向的内容，如果是结构体，获取结构体内容
type GetVariablesRequest struct {
	Type constants.RequestType `json:"type"`
	// 请求序列号
	Sequence  uint                  `json:"sequence"`
	Reference constants.RequestType `json:"reference"`
}
