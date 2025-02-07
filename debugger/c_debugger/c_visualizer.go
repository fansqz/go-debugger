package c_debugger

import (
	"fmt"
	mapset "github.com/deckarep/golang-set"
	"github.com/sirupsen/logrus"
	. "go-debugger/debugger"
	"log"
)

func (c *CDebugger) StructuralVisualize(query *StructVisualQuery) (*StructVisualData, error) {
	valueQuerySet := c.list2set(query.Values)
	pointQuerySet := c.list2set(query.Points)

	// 返回的 指针和可视化结构体
	pointVariables := make([]*VisualVariable, 0, 10)
	// VisualizeNodes 可视化结构的节点列表
	visualizeNodeSet := make(map[string]*VisualNode)
	visualizeNodes := make([]*VisualNode, 0, len(visualizeNodeSet))

	// 读取当前栈帧中的局部变量，如果指向目标结构体则收集
	variables, err := c.GetFrameVariables("0")
	if err != nil {
		return nil, err
	}
	for _, variable := range variables {
		// 如果变量的类型是结构体的指针，那么可以直接遍历去查询所
		if variable.Type == query.Struct+" *" {
			// 获取指向目标结构体的指针
			visualizeVariable := NewVisualVariable(variable)
			visualizeVariable.Value = c.gdbOutputUtil.convertValueToAddress(visualizeVariable.Value)
			pointVariables = append(pointVariables, visualizeVariable)
		}
	}

	// 读取所有栈信息获取所有可视化节点
	if variables, err = c.getAllFrameVariables(); err != nil {
		logrus.Errorf("[StructuralVisualize] getAllFrameVariables fail, err = %v", err)
		return nil, err
	}

	isFirst := true
	// 广度遍历所有目标节点
	for len(variables) != 0 {
		newVariables := make([]*Variable, 0, len(variables))
		for _, variable := range variables {
			// 如果变量的类型是结构体的指针，那么可以直接遍历去查询所
			// isFirst如果不是第一层，就说明是指针域收集起来的，不需要判断类型
			if !isFirst || variable.Type == query.Struct+" *" {
				visualizeNode := &VisualNode{
					ID:   c.gdbOutputUtil.convertValueToAddress(*variable.Value),
					Type: variable.Type,
				}
				if c.gdbOutputUtil.isNullPoint(visualizeNode.ID) {
					continue
				}
				// 如果已经遍历过则不需要处理
				if _, ok := visualizeNodeSet[visualizeNode.ID]; ok {
					continue
				}
				// 获取所有变量
				vars, err := c.GetVariables(variable.Reference)
				if err != nil {
					return nil, err
				}
				// 指针指向的结构体，查询出数据域和指针域
				for _, v := range vars {
					// 处理数据域
					if valueQuerySet.Contains(v.Name) {
						visualizeNode.Values = append(visualizeNode.Values, NewVisualVariable(v))
					} else if pointQuerySet.Contains(v.Name) {
						// 处理指针域
						visualizeVariable := NewVisualVariable(v)
						visualizeVariable.Value = c.gdbOutputUtil.convertValueToAddress(visualizeVariable.Value)
						visualizeNode.Points = append(visualizeNode.Points, visualizeVariable)
						newVariables = append(newVariables, v)
					}
				}
				visualizeNodeSet[visualizeNode.ID] = visualizeNode
				visualizeNodes = append(visualizeNodes, visualizeNode)

			}
		}
		variables = newVariables
		isFirst = false
	}

	return &StructVisualData{
		Points: pointVariables,
		Nodes:  visualizeNodes,
	}, nil
}

func (c *CDebugger) list2set(list []string) mapset.Set {
	set := mapset.NewSet()
	for _, value := range list {
		set.Add(value)
	}
	return set
}

// 读取所有栈帧中的变量列表
func (c *CDebugger) getAllFrameVariables() ([]*Variable, error) {
	variables := make([]*Variable, 0, 10)
	frames, err := c.GetStackTrace()
	if err != nil {
		log.Println(err)
		return nil, err
	}
	for _, frame := range frames {
		vs, err2 := c.GetFrameVariables(frame.ID)
		if err2 != nil {
			log.Println(err)
			return nil, err2
		}
		variables = append(variables, vs...)
	}
	return variables, nil
}

func (c *CDebugger) VariableVisualize(query *VariableVisualQuery) (*VariableVisualData, error) {
	variables, err := c.GetFrameVariables("0")
	if err != nil {
		return nil, err
	}
	pointQuerySet := mapset.NewSet()
	for _, point := range query.PointVars {
		pointQuerySet.Add(point)
	}

	// 返回的 指针和可视化结构体
	pointVariables := make([]*VisualVariable, 0, 10)
	// VisualizeNodes 可视化结构的节点列表
	var structVariables []*Variable
	structVarNameSet := mapset.NewSet()
	for _, structVar := range query.StructVars {
		structVarNameSet.Add(structVar)
	}

	for _, variable := range variables {
		if pointQuerySet.Contains(variable.Name) {
			visualizeVariable := NewVisualVariable(variable)
			pointVariables = append(pointVariables, visualizeVariable)
		}
		if structVarNameSet.Contains(variable.Name) {
			structVariables = append(structVariables, variable)
		}
	}

	// 读取结构体里面的内容
	structs := []*VisualNode{}
	for _, structVariable := range structVariables {
		vars, err := c.GetVariables(structVariable.Reference)
		if err != nil {
			log.Println(err)
			continue
		}
		newVars := make([]*VisualVariable, len(vars))
		for i, va := range vars {
			newVars[i] = NewVisualVariable(va)
		}
		visualizeNode := &VisualNode{
			Name:   structVariable.Name,
			ID:     c.gdbOutputUtil.convertValueToAddress(*structVariable.Value),
			Type:   structVariable.Type,
			Values: newVars,
		}
		structs = append(structs, visualizeNode)
	}

	return &VariableVisualData{
		Points:  pointVariables,
		Structs: structs,
	}, nil
}

func (c *CDebugger) getVariableByName(name string) ([]*Variable, error) {
	return c.GetVariables(convertReference(&referenceStruct{
		Type:         "v",
		FrameId:      "0",
		VariableName: name,
	}))
}

func (g *CDebugger) getArrayVariables(reference string, size int) ([]*Variable, error) {
	// 正则表达式，捕获栈帧ID和变量名
	refStruct, err := parseReference(reference)
	if err != nil {
		return nil, err
	}

	if refStruct.Type == "v" {
		// 如果是普通类型需要切换栈帧，同一个变量名，可能在不同栈帧中会有重复，需要定位栈帧和变量名称才能读取到变量值。
		if _, err = g.sendWithTimeOut(OptionTimeout, "stack-select-frame", refStruct.FrameId); err != nil {
			return nil, err
		}
	}

	// 获取所有children列表并解析
	var m map[string]interface{}

	name := "structName"
	// 创建变量
	if refStruct.Type == "v" {
		m, err = g.sendWithTimeOut(OptionTimeout, "var-create", name, "@",
			refStruct.VariableName)
	} else if refStruct.Type == "p" {
		// 如果是指针类型需要转一下
		typ := refStruct.PointType
		if size != 0 {
			typ = fmt.Sprintf("%s(*)[%d]", typ[0:len(typ)-2], size)
			refStruct.FieldPath = fmt.Sprintf("*(%s)%s", typ, refStruct.Address)
		}
		m, err = g.sendWithTimeOut(OptionTimeout, "var-create", name, "*",
			fmt.Sprintf("(%s)%s", typ, refStruct.Address))
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = g.sendWithTimeOut(OptionTimeout, "var-delete", "structName")
	}()

	varName := name
	if refStruct.FieldPath != "" {
		varName = fmt.Sprintf("%s.%s", name, refStruct.FieldPath)
	}
	m, err = g.sendWithTimeOut(OptionTimeout, "var-list-children", "1",
		varName)
	if err != nil {
		return nil, err
	}
	return g.gdbOutputUtil.parseVariablesOutput(reference, m), nil
}
