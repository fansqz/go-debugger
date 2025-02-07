package go_debugger

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service/api"
	"github.com/sirupsen/logrus"
	. "go-debugger/debugger"
	e "go-debugger/error"
	"go/constant"
	"reflect"
	"strconv"
	"strings"
)

func (g *GoDebugger) GetStackTrace(ctx context.Context) ([]*StackFrame, error) {
	logrus.Infof("[GoDebugger] GetStackTrace")
	if !g.statusManager.Is(Stopped) {
		return nil, e.ErrProgramIsRunningOptionFail
	}
	// 判断目标程序是否还存在
	if _, err := g.delve.Target().Valid(); err != nil {
		return nil, err
	}
	goRotine, err := g.SelectedGoRoutine()
	if err != nil {
		return nil, errors.New("get go routine err")
	}
	stackframes, err := g.delve.Stacktrace(goRotine.ID, 100, api.StacktraceSimple)
	if err != nil {
		return nil, err
	}
	answer := make([]*StackFrame, len(stackframes))
	for i, s := range stackframes {
		answer[i] = &StackFrame{
			ID:   strconv.Itoa(i),
			Name: s.Current.Fn.Name,
			Path: s.Current.File,
			Line: s.Current.Line,
		}
	}
	return answer, nil
}

func (g *GoDebugger) GetFrameVariables(ctx context.Context, frameId string) ([]*Variable, error) {
	logrus.Infof("[GoDebugger] GetFrameVariables")
	if !g.statusManager.Is(Stopped) {
		return nil, e.ErrProgramIsRunningOptionFail
	}
	// 判断目标程序是否还存在
	if _, err := g.delve.Target().Valid(); err != nil {
		return nil, err
	}
	frame, _ := strconv.Atoi(frameId)
	goRotine, err := g.SelectedGoRoutine()
	if err != nil {
		return nil, err
	}
	args, err := g.delve.FunctionArguments(goRotine.ID, frame, 0, LoadConfig)
	args = g.filterArgs(args)
	if err != nil {
		return nil, err
	}
	locals, err := g.delve.LocalVariables(goRotine.ID, frame, 0, LoadConfig)
	if err != nil {
		return nil, err
	}
	locScope := &fullyQualifiedVariable{&proc.Variable{Name: "Locals", Children: slicePtrVarToSliceVar(append(args, locals...))}, "", true, 0}
	ref := g.variableHandles.create(locScope)
	return g.GetVariables(ctx, strconv.Itoa(ref))
}

// filterArgs FunctionArguments获取函数参数的时候，会放回~r0这些返回值参数，使用这个方法进行过滤
func (g *GoDebugger) filterArgs(vs []*proc.Variable) []*proc.Variable {
	answer := make([]*proc.Variable, 0, len(vs))
	for _, v := range vs {
		if !strings.HasPrefix(v.Name, "~r") {
			answer = append(answer, v)
		}
	}
	return answer
}

func (g *GoDebugger) GetVariables(ctx context.Context, reference string) ([]*Variable, error) {
	logrus.Infof("[GoDebugger] GetVariables")
	if !g.statusManager.Is(Stopped) {
		return nil, e.ErrProgramIsRunningOptionFail
	}
	// 判断目标程序是否还存在
	if _, err := g.delve.Target().Valid(); err != nil {
		return nil, err
	}
	ref, _ := strconv.Atoi(reference)
	v, ok := g.variableHandles.get(ref)
	if !ok {
		return nil, errors.New("未找到该引用")
	}

	children := []*Variable{} // must return empty array, not null, if no children

	// 如果类型是结构体指针，那么先获取结构体的引用
	if v.Kind == reflect.Ptr && len(v.Children) == 1 && v.Children[0].Kind == reflect.Struct {
		indexed := g.childrenToDAPVariables(v)
		ref, _ = strconv.Atoi(indexed[0].Reference)
		v, ok = g.variableHandles.get(ref)
		if !ok {
			return nil, errors.New("未找到该引用")
		}
	}

	indexed := g.childrenToDAPVariables(v)
	children = append(children, indexed...)
	return children, nil
}

func slicePtrVarToSliceVar(vars []*proc.Variable) []proc.Variable {
	r := make([]proc.Variable, len(vars))
	for i := range vars {
		r[i] = *vars[i]
	}
	return r
}

const (
	skipRef convertVariableFlags = 1 << iota
	showFullValue
)

const maxMapKeyValueLen = 64
const maxVarValueLen = 1 << 8 // 256

// 获取v的chidren
func (g *GoDebugger) childrenToDAPVariables(v *fullyQualifiedVariable) []*Variable {
	// TODO(polina): consider convertVariableToString instead of convertVariable
	// and avoid unnecessary creation of variable handles when this is called to
	// compute evaluate names when this is called from onSetVariableRequest.
	children := []*Variable{} // must return empty array, not null, if no children

	switch v.Kind {
	case reflect.Map:
		for i := 0; i < len(v.Children); i += 2 {
			// A map will have twice as many children as there are key-value elements.
			kvIndex := i / 2
			// Process children in pairs: even indices are map keys, odd indices are values.
			keyv, valv := &v.Children[i], &v.Children[i+1]
			keyexpr := fmt.Sprintf("(*(*%q)(%#x))", keyv.TypeString(), keyv.Addr)
			valexpr := fmt.Sprintf("%s[%s]", v.fullyQualifiedNameOrExpr, keyexpr)
			switch keyv.Kind {
			// For value expression, use the key value, not the corresponding expression if the key is a scalar.
			case reflect.Bool, reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
				reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				valexpr = fmt.Sprintf("%s[%s]", v.fullyQualifiedNameOrExpr, api.VariableValueAsString(keyv))
			case reflect.String:
				if key := constant.StringVal(keyv.Value); keyv.Len == int64(len(key)) { // fully loaded
					valexpr = fmt.Sprintf("%s[%q]", v.fullyQualifiedNameOrExpr, key)
				}
			}
			key, keyref := g.convertVariable(keyv, keyexpr)
			val, valref := g.convertVariable(valv, valexpr)
			keyType := keyv.TypeString()
			valType := valv.TypeString()
			// If key or value or both are scalars, we can use
			// a single variable to represent key:value format.
			// Otherwise, we must return separate variables for both.
			if keyref > 0 && valref > 0 { // Both are not scalars
				keyvar := Variable{
					Name:      fmt.Sprintf("[key %d]", v.startIndex+kvIndex),
					Type:      keyType,
					Value:     &key,
					Reference: strconv.Itoa(keyref),
				}
				valvar := Variable{
					Name:      fmt.Sprintf("[val %d]", v.startIndex+kvIndex),
					Type:      valType,
					Value:     &val,
					Reference: strconv.Itoa(valref),
				}
				children = append(children, &keyvar, &valvar)
			} else { // At least one is a scalar
				keyValType := valType
				if len(keyType) > 0 && len(valType) > 0 {
					keyValType = fmt.Sprintf("%s: %s", keyType, valType)
				}
				kvvar := Variable{
					Name:  key,
					Type:  keyValType,
					Value: &val,
				}
				if keyref != 0 { // key is a type to be expanded
					if len(key) > maxMapKeyValueLen {
						// Truncate and make unique
						kvvar.Name = fmt.Sprintf("%s... @ %#x", key[0:maxMapKeyValueLen], keyv.Addr)
					}
					kvvar.Reference = strconv.Itoa(keyref)
				} else if valref != 0 { // val is a type to be expanded
					kvvar.Reference = strconv.Itoa(valref)
				}
				children = append(children, &kvvar)
			}
		}
	case reflect.Slice, reflect.Array:
		children = make([]*Variable, len(v.Children))
		for i := range v.Children {
			idx := v.startIndex + i
			cfqname := fmt.Sprintf("%s[%d]", v.fullyQualifiedNameOrExpr, idx)
			cvalue, cvarref := g.convertVariable(&v.Children[i], cfqname)
			children[i] = &Variable{
				Name:      fmt.Sprintf("[%d]", idx),
				Type:      v.Children[i].TypeString(),
				Value:     &cvalue,
				Reference: strconv.Itoa(cvarref),
			}
		}
	default:
		children = make([]*Variable, len(v.Children))
		for i := range v.Children {
			c := &v.Children[i]
			cfqname := fmt.Sprintf("%s.%s", v.fullyQualifiedNameOrExpr, c.Name)

			if strings.HasPrefix(c.Name, "~") || strings.HasPrefix(c.Name, ".") {
				cfqname = ""
			} else if v.isScope && v.fullyQualifiedNameOrExpr == "" {
				cfqname = c.Name
			} else if v.fullyQualifiedNameOrExpr == "" {
				cfqname = ""
			} else if v.Kind == reflect.Interface {
				cfqname = fmt.Sprintf("%s.(%s)", v.fullyQualifiedNameOrExpr, c.Name) // c is data
			} else if v.Kind == reflect.Ptr {
				cfqname = fmt.Sprintf("(*%v)", v.fullyQualifiedNameOrExpr) // c is the nameless pointer value
			} else if v.Kind == reflect.Complex64 || v.Kind == reflect.Complex128 {
				cfqname = "" // complex children are not struct fields and can't be accessed directly
			}
			cvalue, cvarref := g.convertVariable(c, cfqname)

			// Annotate any shadowed variables to "(name)" in order
			// to distinguish from non-shadowed variables.
			// TODO(suzmue): should we support a special evaluateName syntax that
			// can access shadowed variables?
			name := c.Name
			if c.Flags&proc.VariableShadowed == proc.VariableShadowed {
				name = fmt.Sprintf("(%s)", name)
			}

			if v.isScope && v.Name == "Registers" {
				// Align all of the register names.
				name = fmt.Sprintf("%6s", strings.ToLower(c.Name))
				// Set the correct evaluate name for the register.
				cfqname = fmt.Sprintf("_%s", strings.ToUpper(c.Name))
				// Unquote the value
				if ucvalue, err := strconv.Unquote(cvalue); err == nil {
					cvalue = ucvalue
				}
			}

			children[i] = &Variable{
				Name:      name,
				Type:      c.TypeString(),
				Value:     &cvalue,
				Reference: strconv.Itoa(cvarref),
			}
		}
	}
	return children
}

func (g *GoDebugger) convertVariable(v *proc.Variable, qualifiedNameOrExpr string) (value string, variablesReference int) {
	return g.convertVariableWithOpts(v, qualifiedNameOrExpr, 0)
}

type convertVariableFlags uint8

// convertVariableWithOpts allows to skip reference generation in case all we need is
// a string representation of the variable. When the variable is a compound or reference
// type variable and its full string representation can be larger than defaultMaxValueLen,
// this returns a truncated value unless showFull option flag is set.
func (g *GoDebugger) convertVariableWithOpts(v *proc.Variable, qualifiedNameOrExpr string, opts convertVariableFlags) (value string, variablesReference int) {
	canHaveRef := false
	maybeCreateVariableHandle := func(v *proc.Variable) int {
		canHaveRef = true
		if opts&skipRef != 0 {
			return 0
		}
		return g.variableHandles.create(&fullyQualifiedVariable{v, qualifiedNameOrExpr, false /*not a scope*/, 0})
	}
	value = getValueFromVariable(v)
	if v.Unreadable != nil {
		return value, 0
	}

	switch v.Kind {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, _ := strconv.ParseUint(api.ConvertVar(v).Value, 10, 64)
		value = fmt.Sprintf("%s = %#x", value, n)
	case reflect.UnsafePointer:
		// Skip child reference
	case reflect.Ptr:
		if v.DwarfType != nil && len(v.Children) > 0 && v.Children[0].Addr != 0 && v.Children[0].Kind != reflect.Invalid {
			if v.Children[0].OnlyAddr { // Not loaded
				if v.Addr == 0 {
					// This is equivalent to the following with the cli:
					//    (dlv) p &a7
					//    (**main.FooBar)(0xc0000a3918)
					//
					// TODO(polina): what is more appropriate?
					// Option 1: leave it unloaded because it is a special case
					// Option 2: load it, but then we have to load the child, not the parent, unlike all others
					// TODO(polina): see if reloadVariable can be reused here
					cTypeName := api.PrettyTypeName(v.Children[0].DwarfType)
					cLoadExpr := fmt.Sprintf("*(*%q)(%#x)", cTypeName, v.Children[0].Addr)
					cLoaded, err := g.delve.EvalVariableInScope(-1, 0, 0, cLoadExpr, LoadConfig)
					if err != nil {
						value += fmt.Sprintf(" - FAILED TO LOAD: %s", err)
					} else {
						cLoaded.Name = v.Children[0].Name // otherwise, this will be the pointer expression
						v.Children = []proc.Variable{*cLoaded}
						value = "0x" + strconv.FormatUint(v.Addr, 16)
					}
				} else {
					value = g.reloadVariable(v)
				}
			}
			if !v.Children[0].OnlyAddr {
				variablesReference = maybeCreateVariableHandle(v)
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Len > int64(len(v.Children)) { // Not fully loaded
			if v.Base != 0 && len(v.Children) == 0 { // Fully missing
				value = g.reloadVariable(v)
			} else {
				value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children), v.Len) + value
			}
		}
		if v.Base != 0 && len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.Map:
		if v.Len > int64(len(v.Children)/2) { // Not fully loaded
			if len(v.Children) == 0 { // Fully missing
				value = g.reloadVariable(v)
			} else {
				value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children)/2, v.Len) + value
			}
		}
		if v.Base != 0 && len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.String:
		// TODO(polina): implement auto-loading here.
	case reflect.Interface:
		if v.Addr != 0 && len(v.Children) > 0 && v.Children[0].Kind != reflect.Invalid && v.Children[0].Addr != 0 {
			if v.Children[0].OnlyAddr { // Not loaded
				value = g.reloadVariable(v)
			}
			if !v.Children[0].OnlyAddr {
				variablesReference = maybeCreateVariableHandle(v)
			}
		}
	case reflect.Struct:
		if v.Len > int64(len(v.Children)) { // Not fully loaded
			if len(v.Children) == 0 { // Fully missing
				value = g.reloadVariable(v)
			} else { // Partially missing (TODO)
				value = fmt.Sprintf("(loaded %d/%d) ", len(v.Children), v.Len) + value
			}
		}
		if len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	case reflect.Complex64, reflect.Complex128:
		v.Children = make([]proc.Variable, 2)
		v.Children[0].Name = "real"
		v.Children[0].Value = constant.Real(v.Value)
		v.Children[1].Name = "imaginary"
		v.Children[1].Value = constant.Imag(v.Value)
		if v.Kind == reflect.Complex64 {
			v.Children[0].Kind = reflect.Float32
			v.Children[1].Kind = reflect.Float32
		} else {
			v.Children[0].Kind = reflect.Float64
			v.Children[1].Kind = reflect.Float64
		}
		fallthrough
	default: // Complex, Scalar, Chan, Func
		if len(v.Children) > 0 {
			variablesReference = maybeCreateVariableHandle(v)
		}
	}

	// By default, only values of variables that have children can be truncated.
	// If showFullValue is set, then all value strings are not truncated.
	canTruncateValue := showFullValue&opts == 0
	if len(value) > maxVarValueLen && canTruncateValue && canHaveRef {
		value = value[:maxVarValueLen] + "..."
	}
	return value, variablesReference
}

// 根据加载配置，某些类型可能完全或部分未被加载。
// 那些完全缺失的类型（例如由于达到最大变量递归限制），可以在原地重新加载。
func (g *GoDebugger) reloadVariable(v *proc.Variable) (value string) {
	// We might be loading variables from the frame that's not topmost, so use
	// frame-independent address-based expression, not fully-qualified name as per
	// https://github.com/go-delve/delve/blob/master/Documentation/api/ClientHowto.md#looking-into-variables.
	// TODO(polina): Get *proc.Variable object from debug instead. Export a function to set v.loaded to false
	// and call v.loadValue gain with a different load config. It's more efficient, and it's guaranteed to keep
	// working with generics.
	value = getValueFromVariable(v)
	typeName := api.PrettyTypeName(v.DwarfType)
	loadExpr := fmt.Sprintf("*(*%q)(%#x)", typeName, v.Addr)
	// Make sure we can load the pointers directly, not by updating just the child
	// This is not really necessary now because users have no way of setting FollowPointers to false.
	config := LoadConfig
	config.FollowPointers = true
	vLoaded, err := g.delve.EvalVariableInScope(-1, 0, 0, loadExpr, config)
	if err != nil {
		value += fmt.Sprintf(" - FAILED TO LOAD: %s", err)
	} else {
		v.Children = vLoaded.Children
		v.Value = vLoaded.Value
		value = getValueFromVariable(v)
	}
	return value
}

func getValueFromVariable(v *proc.Variable) string {
	// 使用delve中dap的获取值的方法
	value := api.ConvertVar(v).SinglelineString()
	// 如果是指针，则改为指针，那么取该指针指向的地址作为value
	if v.Kind == reflect.Ptr {
		if len(v.Children) == 0 {
			value = "0x0"
		} else {
			value = "0x" + strconv.FormatUint(v.Children[0].Addr, 16)
		}
	}
	return value
}
