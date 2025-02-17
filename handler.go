package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"go-debugger/constants"
	. "go-debugger/debugger"
	"go-debugger/debugger/go_debugger"
	e "go-debugger/error"
	"go-debugger/protocol"
	"go-debugger/utils"
	"net"
	"os"
	"path"
	"time"
)

const (
	CompileTimeout = time.Duration(100000)
	OptionTimeout  = time.Duration(100000)
)

type DebuggerHandler struct {
	debugger Debugger
	language constants.LanguageType
}

func NewDebuggerHandler() *DebuggerHandler {
	return &DebuggerHandler{}
}

func (d *DebuggerHandler) handle(conn net.Conn, req []byte) {
	ctx := context.Background()
	type reqStruct struct {
		Type     constants.RequestType `json:"type"`
		Sequence uint                  `json:"sequence"`
	}
	r := &reqStruct{}
	// 判断请求类型
	if err := json.Unmarshal(req, &r); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		return
	}
	if d.debugger == nil && r.Type != constants.StartDebug {
		d.sendResponse(conn, r.Sequence, false, "debug not start", nil)
	}
	// 获取
	switch r.Type {
	case constants.StartDebug:
		// 启动调试
		callback := func(event interface{}) {
			answer, err := json.Marshal(event)
			if err != nil {
				logrus.Errorf("marshal event fail, err = %v", err)
				return
			}
			conn.Write(answer)
		}
		d.handleStartDebugRequest(ctx, conn, req, callback)
	case constants.SendToConsole:
		// 发送命令到控制台
		d.handleSendToConsoleRequest(ctx, conn, req)
	case constants.Step:
		// 单步调试
		d.handleStepRequest(ctx, conn, req)
	case constants.Continue:
		// continue
		d.handleContinueRequest(ctx, conn, req)
	case constants.Terminate:
		d.handleTerminalRequest(ctx, conn, req)
	case constants.AddBreakpoints:
		d.handleAddBreakpointRequest(ctx, conn, req)
	case constants.RemoveBreakpoints:
		d.handleRemoveBreakpointRequest(ctx, conn, req)
	case constants.StackTrace:
		d.handleGetStackTraceRequest(ctx, conn, req)
	case constants.FrameVariables:
		d.handleGetFrameVariables(ctx, conn, req)
	default:
		d.sendResponse(conn, r.Sequence, false, "request type not support", nil)
	}
}

func (d *DebuggerHandler) sendResponse(conn net.Conn, sequence uint, success bool, message string, body interface{}) {
	response := &protocol.Response{
		Sequence: sequence,
		Success:  success,
		Message:  message,
		Data:     body,
	}
	answer, err := json.Marshal(response)
	if err != nil {
		logrus.Warnf("marshal reponse fail, err = %v", err)
	}
	conn.Write(answer)
}

func (d *DebuggerHandler) handleSendToConsoleRequest(ctx context.Context, conn net.Conn, req []byte) {
	sendToConsoleReq := protocol.SendToConsoleRequest{}
	if err := json.Unmarshal(req, &sendToConsoleReq); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, sendToConsoleReq.Sequence, false, err.Error(), nil)
		return
	}
	if err := d.debugger.Send(ctx, sendToConsoleReq.Content); err != nil {
		d.sendResponse(conn, sendToConsoleReq.Sequence, false, err.Error(), nil)
		return
	}
	d.sendResponse(conn, sendToConsoleReq.Sequence, true, "", nil)
}

func (d *DebuggerHandler) handleStepRequest(ctx context.Context, conn net.Conn, req []byte) {
	stepReq := protocol.StepRequest{}
	if err := json.Unmarshal(req, &stepReq); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, stepReq.Sequence, false, err.Error(), nil)
		return
	}
	var err error
	if stepReq.StepType == constants.StepIn {
		err = d.debugger.StepIn(ctx)
	} else if stepReq.StepType == constants.StepOver {
		err = d.debugger.StepOver(ctx)
	} else if stepReq.StepType == constants.StepOut {
		err = d.debugger.StepOut(ctx)
	} else {
		err = fmt.Errorf("step type not support")
	}
	if err != nil {
		d.sendResponse(conn, stepReq.Sequence, false, err.Error(), nil)
		return
	}
	d.sendResponse(conn, stepReq.Sequence, true, "", nil)
}

func (d *DebuggerHandler) handleContinueRequest(ctx context.Context, conn net.Conn, req []byte) {
	continueReq := protocol.ContinueRequest{}
	if err := json.Unmarshal(req, &continueReq); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, continueReq.Sequence, false, err.Error(), nil)
		return
	}
	if err := d.debugger.Continue(ctx); err != nil {
		d.sendResponse(conn, continueReq.Sequence, false, err.Error(), nil)
		return
	}
	d.sendResponse(conn, continueReq.Sequence, true, "", nil)
}

func (d *DebuggerHandler) handleTerminalRequest(ctx context.Context, conn net.Conn, req []byte) {
	terminalReq := protocol.TerminateRequest{}
	if err := json.Unmarshal(req, &terminalReq); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, terminalReq.Sequence, false, err.Error(), nil)
		return
	}
	if err := d.debugger.Terminate(ctx); err != nil {
		d.sendResponse(conn, terminalReq.Sequence, false, err.Error(), nil)
		// 无论是否成功，清理debugger
		d.debugger = nil
		return
	}
	d.sendResponse(conn, terminalReq.Sequence, true, "", nil)
	d.debugger = nil
}

func (d *DebuggerHandler) handleStartDebugRequest(ctx context.Context, conn net.Conn, req []byte, callback NotificationCallback) {
	startReq := protocol.StartDebugRequest{}
	if err := json.Unmarshal(req, &startReq); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, startReq.Sequence, false, err.Error(), nil)
		return
	}
	if startReq.Language == constants.LanguageGo {
		d.debugger = go_debugger.NewGoDebugger()
		d.language = constants.LanguageGo
	}
	// 存储文件
	workPath := getWorkPath()
	if err := os.MkdirAll(workPath, os.ModePerm); err != nil {
		logrus.Errorf("[Start] make dir error, err = %v", err)
		d.sendResponse(conn, startReq.Sequence, false, err.Error(), nil)
		return
	}
	// 保存用户代码到用户的执行路径
	var compileFiles []string
	var err error
	if compileFiles, err = saveUserCode(ctx, startReq.Language, startReq.Code, workPath); err != nil {
		d.sendResponse(conn, startReq.Sequence, false, err.Error(), nil)
		return
	}

	// 断点类型转换
	breakpoints := make([]*Breakpoint, len(startReq.Breakpoints))
	mainFile, _ := getMainFileNameByLanguage(startReq.Language)
	for i, bp := range startReq.Breakpoints {
		breakpoints[i] = &Breakpoint{
			File: mainFile,
			Line: bp,
		}
	}

	// 启动调试
	err = d.debugger.Start(ctx, &StartOption{
		CompileTimeout: CompileTimeout,
		CompileFiles:   compileFiles,
		WorkPath:       workPath,
		BreakPoints:    breakpoints,
		Callback:       callback,
	})
	if err != nil {
		d.sendResponse(conn, startReq.Sequence, false, err.Error(), nil)
		return
	}
	d.sendResponse(conn, startReq.Sequence, true, "", nil)
}

func (d *DebuggerHandler) handleAddBreakpointRequest(ctx context.Context, conn net.Conn, reqData []byte) {
	req := protocol.AddBreakpointRequest{}
	if err := json.Unmarshal(reqData, &req); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}
	bps := make([]*Breakpoint, len(req.Breakpoints))
	mainFile, err := getMainFileNameByLanguage(d.language)
	if err != nil {
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}
	for i, breakpoint := range req.Breakpoints {
		bps[i] = &Breakpoint{
			File: mainFile,
			Line: breakpoint,
		}
	}
	if err = d.debugger.AddBreakpoints(ctx, bps); err != nil {
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}
	d.sendResponse(conn, req.Sequence, true, "", nil)
}

func (d *DebuggerHandler) handleRemoveBreakpointRequest(ctx context.Context, conn net.Conn, reqData []byte) {
	req := protocol.AddBreakpointRequest{}
	if err := json.Unmarshal(reqData, &req); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}
	bps := make([]*Breakpoint, len(req.Breakpoints))
	mainFile, err := getMainFileNameByLanguage(d.language)
	if err != nil {
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}
	for i, breakpoint := range req.Breakpoints {
		bps[i] = &Breakpoint{
			File: mainFile,
			Line: breakpoint,
		}
	}
	if err = d.debugger.RemoveBreakpoints(ctx, bps); err != nil {
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}
	d.sendResponse(conn, req.Sequence, true, "", nil)
}

func (d *DebuggerHandler) handleGetStackTraceRequest(ctx context.Context, conn net.Conn, reqData []byte) {
	req := protocol.GetStackTrace{}
	if err := json.Unmarshal(reqData, &req); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}

	answer, err := d.debugger.GetStackTrace(ctx)
	if err != nil {
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}
	d.sendResponse(conn, req.Sequence, true, "", answer)
}

func (d *DebuggerHandler) handleGetFrameVariables(ctx context.Context, conn net.Conn, reqData []byte) {
	req := protocol.GetFrameVariables{}
	if err := json.Unmarshal(reqData, &req); err != nil {
		logrus.Warnf("parse request error, err = %v", err)
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}

	answer, err := d.debugger.GetFrameVariables(ctx, req.FrameID)
	if err != nil {
		d.sendResponse(conn, req.Sequence, false, err.Error(), nil)
		return
	}
	d.sendResponse(conn, req.Sequence, true, "", answer)
}

// saveUserCode
// 保存用户代码到用户的executePath，并返回需要编译的文件列表
func saveUserCode(ctx context.Context, language constants.LanguageType, codeStr string, executePath string) ([]string, error) {
	var compileFiles []string
	var mainFile string
	var err error

	if mainFile, err = getMainFileNameByLanguage(language); err != nil {
		return nil, err
	}
	if err = os.WriteFile(path.Join(executePath, mainFile), []byte(codeStr), 0644); err != nil {
		return nil, err
	}
	// 将main文件进行编译即可
	compileFiles = []string{path.Join(executePath, mainFile)}

	return compileFiles, nil
}

// 根据编程语言获取该编程语言的Main文件名称
func getMainFileNameByLanguage(language constants.LanguageType) (string, error) {
	switch language {
	case constants.LanguageC:
		return "main.c", nil
	case constants.LanguageJava:
		return "Main.java", nil
	case constants.LanguageGo:
		return "main.go", nil
	default:
		return "", e.ErrLanguageNotSupported
	}
}

// getWorkPath 给用户的此次运行生成一个临时目录
func getWorkPath() string {
	uuid := utils.GetUUID()
	executePath := path.Join("/debug/", uuid)
	return executePath
}
