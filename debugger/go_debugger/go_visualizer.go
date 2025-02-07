package go_debugger

import (
	"context"
	"fmt"
	mapset "github.com/deckarep/golang-set"
	"github.com/sirupsen/logrus"
	. "go-debugger/debugger"
	"go-debugger/utils"
	"log"
	"regexp"
)

func (c *GoDebugger) StructVisual(ctx context.Context, query *StructVisualQuery) (*StructVisualData, error) {
	logrus.Infof("[GoDebugger] StructVisual")
	valueQuerySet := utils.List2set(query.Values)
	pointQuerySet := utils.List2set(query.Points)

	// 返回的 指针和可视化结构体
	pointVariables := make([]*VisualVariable, 0, 10)
	// VisualNodes 可视化结构的节点列表
	visualNodeSet := make(map[string]*VisualNode)
	visualNodes := make([]*VisualNode, 0, len(visualNodeSet))

	// 收集指针
	variables, err := c.GetFrameVariables(ctx, "0")
	if err != nil {
		return nil, err
	}
	regexpStr := fmt.Sprintf(`^\*.*\.%s.*$`, query.Struct)
	re := regexp.MustCompile(regexpStr)
	for _, variable := range variables {
		// 如果变量的类型是结构体的指针，那么可以直接遍历去查询所
		if re.MatchString(variable.Type) {
			// 获取指向目标结构体的指针
			visualVariable := NewVisualVariable(variable)
			pointVariables = append(pointVariables, visualVariable)
		}
	}

	// 读取目标结构体
	var allVariables []*Variable
	if allVariables, err = c.getAllFrameVariables(ctx); err != nil {
		logrus.Errorf("[StructVisual] getAllFrameVariables fail, err = %v", err)
		return nil, err
	}
	variables = make([]*Variable, 0, len(allVariables))
	for _, v := range allVariables {
		if re.MatchString(v.Type) {
			variables = append(variables, v)
		}
	}

	// 广度遍历所有目标节点
	for len(variables) != 0 {
		newVariables := make([]*Variable, 0, len(variables))
		for _, variable := range variables {
			// isFirst如果不是第一层，就说明是指针域收集起来的，不需要判断类型
			value := ""
			if variable.Value != nil {
				value = *variable.Value
			}
			visualNode := &VisualNode{
				ID:   value,
				Type: variable.Type,
			}

			if variable.Reference == "" || variable.Reference == "0" {
				continue
			}
			if _, ok := visualNodeSet[visualNode.ID]; ok {
				continue
			}

			// 读取结构体
			vars, err := c.GetVariables(ctx, variable.Reference)
			if err != nil {
				return nil, err
			}
			// 指针指向的结构体，查询出数据域和指针域
			for _, v := range vars {
				// 处理数据域
				if valueQuerySet.Contains(v.Name) {
					visualNode.Values = append(visualNode.Values, NewVisualVariable(v))
				} else if pointQuerySet.Contains(v.Name) {
					// 处理指针域
					visualVariable := NewVisualVariable(v)
					if v.Value != nil {
						visualVariable.Value = *v.Value
					}
					visualNode.Points = append(visualNode.Points, visualVariable)
					newVariables = append(newVariables, v)
				}
			}
			visualNodeSet[visualNode.ID] = visualNode
			visualNodes = append(visualNodes, visualNode)

		}
		variables = newVariables
	}

	return &StructVisualData{
		Points: pointVariables,
		Nodes:  visualNodes,
	}, nil
}

func (g *GoDebugger) VariableVisual(ctx context.Context, query *VariableVisualQuery) (*VariableVisualData, error) {
	logrus.Infof("[GoDebugger] VariableVisual")
	variables, err := g.GetFrameVariables(ctx, "0")
	if err != nil {
		return nil, err
	}
	pointQuerySet := mapset.NewSet()
	for _, point := range query.PointVars {
		pointQuerySet.Add(point)
	}

	// 返回的 指针和可视化结构体
	pointVariables := make([]*VisualVariable, 0, 10)
	// VisualNodes 可视化结构的节点列表
	var structVariables []*Variable
	structVarNameSet := mapset.NewSet()
	for _, structVar := range query.StructVars {
		structVarNameSet.Add(structVar)
	}

	for _, variable := range variables {
		if pointQuerySet.Contains(variable.Name) {
			visualVariable := NewVisualVariable(variable)
			pointVariables = append(pointVariables, visualVariable)
		}
		if structVarNameSet.Contains(variable.Name) {
			structVariables = append(structVariables, variable)
		}
	}

	// 读取结构体里面的内容
	structs := []*VisualNode{}
	for _, structVariable := range structVariables {
		vars, err := g.GetVariables(ctx, structVariable.Reference)
		if err != nil {
			log.Println(err)
			continue
		}
		newVars := make([]*VisualVariable, len(vars))
		for i, va := range vars {
			newVars[i] = NewVisualVariable(va)
		}
		visualNode := &VisualNode{
			Name:   structVariable.Name,
			Type:   structVariable.Type,
			Values: newVars,
		}
		structs = append(structs, visualNode)
	}

	return &VariableVisualData{
		Points:  pointVariables,
		Structs: structs,
	}, nil
}

// 读取所有栈帧中的变量列表
func (g *GoDebugger) getAllFrameVariables(ctx context.Context) ([]*Variable, error) {
	variables := make([]*Variable, 0, 10)
	frames, err := g.GetStackTrace(ctx)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	for _, frame := range frames {
		vs, err2 := g.GetFrameVariables(ctx, frame.ID)
		if err2 != nil {
			log.Println(err)
			return nil, err2
		}
		variables = append(variables, vs...)
	}
	return variables, nil
}
