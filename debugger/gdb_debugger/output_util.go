package gdb_debugger

import (
	"fmt"
	"github.com/fansqz/go-debugger/constants"
	"github.com/fansqz/go-debugger/debugger/gdb_debugger/gdb"
	"github.com/google/go-dap"
	"github.com/sirupsen/logrus"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// GDBOutputUtil 处理gdb输出的工具
type GDBOutputUtil struct {
	// 保证map的线程安全
	lock sync.RWMutex
}

func NewGDBOutputUtil() *GDBOutputUtil {
	return &GDBOutputUtil{}
}

// parseAddBreakpointOutput 解析添加断点输出
// class->done
//
//	payload->{
//		bkpt—>{
//			  number -> 1
//			  type -> breakpoint
//			  disp -> keep
//			  enabled -> y
//			  func -> main
//			  fullname -> /var/fanCode/tempDir/56370c2d-6d34-11ef-9e80-5a7990d94760/main.c
//			  line -> 43
//			  thread-groups -> len:1, cap:1
//			  addr -> 0x0000000000000806
//			  file -> /var/fanCode/tempDir/56370c2d-6d34-11ef-9e80-5a7990d94760/main.c
//			  times -> 0
//			  original-location -> /var/fanCode/tempDir/56370c2d-6d34-11ef-9e80-5a7990d94760/main.c:43
//			}
func (g *GDBOutputUtil) parseAddBreakpointOutput(m map[string]interface{}) (bool, string) {
	// 处理响应
	bkpts, success := g.getPayloadFromMap(m)
	if !success {
		return false, ""
	}
	// 读取断点
	var breakpoint map[string]interface{}
	bkpt := bkpts.(map[string]interface{})
	if bkpt2, ok := bkpt["bkpt"]; ok {
		if breakpoint, ok = bkpt2.(map[string]interface{}); !ok {
			return false, ""
		}
	}
	// 设置map
	number := g.getStringFromMap(breakpoint, "number")
	g.lock.Lock()
	defer g.lock.Unlock()
	return true, number
}

// parseRemoveBreakpointOutput 解析移除断点输出
// class -> done
func (g *GDBOutputUtil) parseRemoveBreakpointOutput(m map[string]interface{}) bool {
	if class := g.getStringFromMap(m, "class"); class == "done" {
		return true
	}
	return false
}

// parseStackTraceOutput 解析栈帧输出
// class->done
//
//	payload-> {
//	 stack->[
//	  {
//	    frame->{
//	     level->0
//	     addr->0x000055555540081b
//	     func->main
//	     file->/var/fanCode/tempDir/c963bc6a-6d42-11ef-9fa0-5a7990d94760/main.c
//	     fullname->/var/fanCode/tempDir/c963bc6a-6d42-11ef-9fa0-5a7990d94760/main.c
//	     line->44
//	    }
//	  }
//	 ]
//	}
func (g *GDBOutputUtil) parseStackTraceOutput(m map[string]interface{}) []dap.StackFrame {
	answer := make([]dap.StackFrame, 0, 5)
	stackMap, success := g.getPayloadFromMap(m)
	if !success {
		return []dap.StackFrame{}
	}
	stackList := g.getListFromMap(stackMap, "stack")
	for _, s := range stackList {
		frame := g.getInterfaceFromMap(s, "frame")
		id, _ := strconv.Atoi(g.getStringFromMap(frame, "level"))
		fun := g.getStringFromMap(frame, "func")
		line := g.getIntFromMap(frame, "line")
		fullname := g.getStringFromMap(frame, "fullname")
		stack := dap.StackFrame{
			Id:   id,
			Name: fun,
			Line: line,
			Source: &dap.Source{
				Name: filepath.Base(fullname),
				Path: fullname,
			},
		}
		answer = append(answer, stack)
	}
	return answer
}

// parseFrameVariablesOutput 解析获取栈帧变量列表的输出
// class->done
//
//	payload->{
//	 variables->[
//	  {
//	   name->root
//	   type->struct TreeNode *
//	   value->0x555555602260
//	  },
//	 ]
//	}
func (g *GDBOutputUtil) parseFrameVariablesOutput(gdb *gdb.Gdb, m map[string]interface{}) []dap.Variable {
	payload, success := g.getPayloadFromMap(m)
	if !success {
		return []dap.Variable{}
	}
	variables := g.getListFromMap(payload, "variables")
	if variables == nil {
		variables = g.getListFromMap(payload, "locals")
	}
	answer := make([]dap.Variable, 0, 10)
	for _, v := range variables {
		name := g.convertVariableName(g.getStringFromMap(v, "name"))
		m2, err := gdb.SendWithTimeout(OptionTimeout, "var-create", name, "*", name)
		if err != nil {
			logrus.Errorf("getChidrenNumber fail err = %s", err)
		}
		variable := g.parseVarCreate(m2)
		answer = append(answer, *variable)
		_, _ = gdb.SendWithTimeout(OptionTimeout, "var-delete", name)
	}
	return answer
}

// parseGlobalVariableOutput 解析全局变量获取的输出
// class -> done
//
//	payload -> {
//	 symbols -> {
//	   debug -> [{
//	     filename -> /var/fanCode/tempDir/ba7f078a-08da-11f0-8b21-00155db6c6d0/main.c
//	     fullname -> /var/fanCode/tempDir/ba7f078a-08da-11f0-8b21-00155db6c6d0/main.c
//	     symbols -> [
//	       {
//	         line -> 25
//	         name -> globalChar
//	         type -> char
//	         description -> char globalChar;
//	       }
//	     ]
//	   }]
//	 }
//
// }
func (g *GDBOutputUtil) parseGlobalVariableOutput(gdb *gdb.Gdb, m map[string]interface{}) []dap.Variable {
	payload, success := g.getPayloadFromMap(m)
	if !success {
		return []dap.Variable{}
	}
	symbols := g.getInterfaceFromMap(payload, "symbols")
	debug := g.getListFromMap(symbols, "debug")
	answer := make([]dap.Variable, 0, 10)
	for _, t := range debug {
		filename := g.getStringFromMap(t, "filename")
		if strings.HasSuffix(filename, "main.c") || strings.HasSuffix(filename, "main.cpp") || strings.HasSuffix(filename, "main") {
			vars := g.getListFromMap(t, "symbols")
			for _, v := range vars {
				name := g.convertVariableName(g.getStringFromMap(v, "name"))
				m2, err := gdb.SendWithTimeout(OptionTimeout, "var-create", name, "*", name)
				if err != nil {
					logrus.Errorf("getChidrenNumber fail err = %s", err)
				}
				variable := g.parseVarCreate(m2)
				answer = append(answer, *variable)
				_, _ = gdb.SendWithTimeout(OptionTimeout, "var-delete", name)
			}
		}
	}
	return answer
}

// parseVariablesOutput 解析获取变量内容的output
// class->done
//
//	payload->{
//	 numchild->3
//	 children->[
//	  {
//	   child->{
//	    name->structName.left
//	    exp->left
//	    numchild->3
//	    value->0x0
//	    type->struct TreeNode *
//	   }
//	  },
//	 ]
//	}
func (g *GDBOutputUtil) parseVariablesOutput(m map[string]interface{}) []dap.Variable {
	payload, success := g.getPayloadFromMap(m)
	if !success {
		return []dap.Variable{}
	}
	children := g.getListFromMap(payload, "children")
	answer := make([]dap.Variable, 0, 10)
	for _, v := range children {
		v = g.getInterfaceFromMap(v, "child")
		field := dap.Variable{
			Name: g.convertVariableName(g.getStringFromMap(v, "name")),
			Type: g.getStringFromMap(v, "type"),
		}
		if g.checkKeyFromMap(v, "value") {
			value := g.getStringFromMap(v, "value")
			field.Value = value
		}
		if g.checkKeyFromMap(v, "numchild") {
			field.IndexedVariables = g.getIntFromMap(v, "numchild")
		}
		answer = append(answer, field)
	}
	return answer
}

// parseBreakpointHitEventOutput 解析stopped事件中，停留在断点的event输出
// reason->breakpoint-hit
// disp->keep
// bkptno->1
//
//	frame->{
//	 addr -> 0x0000555555400806
//	 func -> main
//	 args -> len:0, cap:0
//	 file -> /var/fanCode/tempDir/3506bc97-6db8-11ef-aba0-5a7990d94760/main.c
//	 fullname -> /var/fanCode/tempDir/3506bc97-6db8-11ef-aba0-5a7990d94760/main.c
//	 line -> 43
//	}
//
// thread-id->1
// stopped-threads->all
// core->4
func (g *GDBOutputUtil) parseStoppedEventOutput(m interface{}) *StoppedOutput {
	r := g.getStringFromMap(m, "reason")
	if r == "breakpoint-hit" {
		frame := g.getInterfaceFromMap(m, "frame")
		fullname := g.getStringFromMap(frame, "fullname")
		lineStr := g.getStringFromMap(frame, "line")
		line, _ := strconv.Atoi(lineStr)
		return &StoppedOutput{
			reason: constants.BreakpointStopped,
			file:   fullname,
			line:   line,
		}
	} else if r == "end-stepping-range" || r == "function-finished" {
		frame := g.getInterfaceFromMap(m, "frame")
		fullname := g.getStringFromMap(frame, "fullname")
		lineStr := g.getStringFromMap(frame, "line")
		line, _ := strconv.Atoi(lineStr)
		return &StoppedOutput{
			reason: constants.StepStopped,
			file:   fullname,
			line:   line,
		}
	} else if r == "exited-normally" {
		return &StoppedOutput{
			reason: constants.ExitedNormally,
		}
	} else {
		return nil
	}
}

// parseVarCreate 解析var-create响应
// class -> done
//
//	payload -> {
//	  name -> name
//	  numchild -> 50
//	  value -> [50]
//	  type -> char [50]
//	  has_more -> 0
//	}
func (g *GDBOutputUtil) parseVarCreate(m map[string]interface{}) *dap.Variable {
	payload, success := g.getPayloadFromMap(m)
	if !success {
		return nil
	}
	variable := &dap.Variable{}
	variable.Name = g.getStringFromMap(payload, "name")
	variable.Value = g.getStringFromMap(payload, "value")
	variable.Type = g.getStringFromMap(payload, "type")
	variable.IndexedVariables = g.getIntFromMap(payload, "numchild")
	return variable
}

type StoppedOutput struct {
	reason constants.StoppedReasonType
	file   string
	line   int
}

// convertVariableName 解析变量名称
// 由于某些结构体或者指针返回的名称不太美观，所以在这里进行一个转换
// 比如获取一个结构体的属性，属性名：localItem.id  ->  id
// 解引用情况：dynamicInt.*(int *)0x555555602260 -> *dynamicInt
// 数组情况：array.0 -> 0
func (g *GDBOutputUtil) convertVariableName(variableName string) string {
	index := strings.LastIndex(variableName, ".")
	if index == -1 {
		return variableName
	}
	if variableName[index+1] == '*' {
		return fmt.Sprintf("*%s", variableName[0:index-1])
	}
	if variableName[index+1] >= '0' && variableName[index+1] <= '9' {
		return variableName[index+1:]
	}
	return variableName[index+1:]
}

// isShouldBeFilterAddress gdb在读取一些变量的时候，会读取到一些初始的数据，需要过滤掉这些数据
func (g *GDBOutputUtil) isShouldBeFilterAddress(address string) bool {
	if strings.HasSuffix(address, "<_start>") {
		return true
	}
	re := regexp.MustCompile(`<__libc_csu_init.*>$`)
	return re.MatchString(address)
}

func (g *GDBOutputUtil) checkIsAddress(value string) bool {
	// 识别c++中的 std::unique_ptr<Item> = {get() = 0x55555556ceb0}
	if strings.HasPrefix(value, "std::unique_ptr") {
		return true
	}
	a := strings.Split(value, " ")
	if len(a) < 1 {
		return false
	}
	pattern := "^0x[0-9a-fA-F]+$"
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(a[0])
}

// convertValueToAddress 从输入字符串中提取以 0x 开头的十六进制地址
func (g *GDBOutputUtil) convertValueToAddress(value string) string {
	// 定义正则表达式模式，用于匹配 0x 开头，后面跟随一位或多位十六进制数字的地址
	re := regexp.MustCompile(`0x[0-9a-fA-F]+`)
	match := re.FindString(value)
	return match
}

// isNullPoint 判断是否是空指针
// 0x0为空指针。解析16进制，如果为0则为null
func (g *GDBOutputUtil) isNullPoint(address string) bool {
	if address == "" || address == "0x0" || address == "0x000000000000" {
		return true
	}
	num, _ := strconv.ParseInt(address, 0, 64)
	return num == 0
}

func (g *GDBOutputUtil) getInterfaceFromMap(m interface{}, key string) interface{} {
	s, ok := m.(map[string]interface{})
	if !ok {
		return nil
	}
	answer, _ := s[key]
	return answer
}

func (g *GDBOutputUtil) getStringFromMap(m interface{}, key string) string {
	answer := g.getInterfaceFromMap(m, key)
	if answer == nil {
		return ""
	}
	strAnswer, _ := answer.(string)
	return strAnswer
}

func (g *GDBOutputUtil) getIntFromMap(m interface{}, key string) int {
	answer := g.getStringFromMap(m, key)
	numAnswer, _ := strconv.Atoi(answer)
	return numAnswer
}

func (g *GDBOutputUtil) getListFromMap(m interface{}, key string) []interface{} {
	s, _ := m.(map[string]interface{})[key]
	s2, _ := s.([]interface{})
	return s2
}

func (g *GDBOutputUtil) mapSet(m interface{}, key string, value string) {
	m2, _ := m.(map[string]interface{})
	m2[key] = value
}

func (g *GDBOutputUtil) mapDelete(m interface{}, key string) {
	m2, _ := m.(map[string]interface{})
	delete(m2, key)
}

// 检查map中是否有某个key
func (g *GDBOutputUtil) checkKeyFromMap(m interface{}, key string) bool {
	s, _ := m.(map[string]interface{})
	_, exist := s[key]
	return exist
}

func (g *GDBOutputUtil) getPayloadFromMap(m map[string]interface{}) (interface{}, bool) {
	if class := g.getStringFromMap(m, "class"); class == "done" {
		if payload, ok := m["payload"]; ok {
			return payload, true
		} else {
			return nil, false
		}
	}
	return nil, false
}
