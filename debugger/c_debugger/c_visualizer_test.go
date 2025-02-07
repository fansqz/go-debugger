package c_debugger

//
//import (
//	"fmt"
//	"github.com/stretchr/testify/assert"
//	"go-debugger/constants"
//	"go-debugger/debugger"
//	"log"
//	"os"
//	"testing"
//)
//
//// 测试树
//func Test_StructuralVisualize(t *testing.T) {
//	var cha = make(chan interface{}, 10)
//	// 创建工作目录, 用户的临时文件
//	executePath := getExecutePath("/var/fanCode/tempDir")
//	defer os.RemoveAll(executePath)
//	if err := os.MkdirAll(executePath, os.ModePerm); err != nil {
//		log.Printf("MkdirAll error: %v\n", err)
//		return
//	}
//	// 保存用户代码到用户的执行路径，并获取编译文件列表
//	var compileFiles []string
//	var err2 *e.Error
//	code := `#include <stdio.h> // 1
//#include <stdlib.h> // 2
//// 定义二叉树节点的结构体 // 3
//typedef struct TreeNode { // 4
//    int data; // 5
//    struct TreeNode *left; // 6
//    struct TreeNode *right; // 7
//} TreeNode; // 8
//// 函数原型 // 9
//TreeNode* createNode(int data); // 10
//TreeNode* insertNode(TreeNode* root, int data); // 11
//int main() { // 12
//    // 创建根节点 // 13
//    TreeNode *root = NULL; // 14
//    // 插入节点来创建树 // 15
//    root = insertNode(root, 5); // 16
//    insertNode(root, 3); // 17
//    insertNode(root, 8); // 18
//    insertNode(root, 1); // 19
//    insertNode(root, 4); // 20
//    insertNode(root, 7); // 21
//    insertNode(root, 9); // 22
//    return 0; // 23
//} // 24
//TreeNode* createNode(int data) { // 25
//    TreeNode* newNode = (TreeNode*)malloc(sizeof(TreeNode)); // 26
//    if (!newNode) { // 27
//        fprintf(stderr, "Memory allocation failed\n"); // 28
//        exit(1); // 29
//    } // 30
//    newNode->data = data; // 31
//    newNode->left = newNode->right = NULL; // 32
//    return newNode; // 33
//} // 34
//// 向二叉树插入新节点 // 35
//TreeNode* insertNode(TreeNode* root, int data) { // 36
//    // 如果树是空的，返回一个新节点 // 37
//    if (root == NULL) { // 38
//        return createNode(data); // 39
//    } // 40
//    // 否则，递归下去 // 41
//    if (data < root->data) { // 42
//        root->left = insertNode(root->left, data); // 43
//    } else if (data > root->data) { // 44
//        root->right = insertNode(root->right, data); // 45
//    } // 46
//    // 返回未变的节点指针 // 47
//    return root; // 48
//} // 49`
//	if compileFiles, err2 = saveUserCode(constants.LanguageC,
//		code, executePath); err2 != nil {
//		log.Println(err2)
//		return
//	}
//	debugNotificationCallback := func(data interface{}) {
//		cha <- data
//	}
//	debug := NewGdbDebugger()
//	err := debug.Start(&debugger.StartOption{
//		WorkPath:     executePath,
//		CompileFiles: compileFiles,
//		GdbCallback:  debugNotificationCallback,
//		BreakPoints:  []*debugger.Breakpoint{{"/main.c", 22}},
//	})
//	assert.Nil(t, err)
//	// 接受调试编译成功信息
//	data := <-cha
//	assert.Equal(t, &debugger.CompileEvent{
//		Success: true,
//		Message: "用户代码编译成功",
//	}, data)
//	data = <-cha
//	assert.Equal(t, &debugger.LaunchEvent{
//		Success: true,
//		Message: "目标代码加载成功",
//	}, data)
//
//	// 添加断点
//	data = <-cha
//	assert.Equal(t, &debugger.BreakpointEvent{
//		Reason:      constants.NewType,
//		Breakpoints: []*debugger.Breakpoint{{"/main.c", 22}},
//	}, data)
//
//	// 启动用户程序
//	<-cha
//	<-cha
//
//	// 读取可视化结构
//	visualizeData, err := debug.StructuralVisualize(&debugger.StructuralVisualizeQuery{
//		Struct: "TreeNode",
//		Points: []string{"left", "right"},
//		Values: []string{"data"},
//	})
//	fmt.Println(visualizeData)
//	assert.NotNil(t, visualizeData)
//	assert.NotEqual(t, len(visualizeData.Points), 0)
//	assert.NotEqual(t, len(visualizeData.Nodes), 0)
//}
//
//// 测试数组
//func Test_VariablePointVisualize(t *testing.T) {
//	var cha = make(chan interface{}, 10)
//	// A创建工作目录, 用户的临时文件
//	executePath := getExecutePath("/var/fanCode/tempDir")
//	defer os.RemoveAll(executePath)
//	if err := os.MkdirAll(executePath, os.ModePerm); err != nil {
//		log.Printf("MkdirAll error: %v\n", err)
//		return
//	}
//	// 保存用户代码到用户的执行路径，并获取编译文件列表
//	var compileFiles []string
//	var err2 *e.Error
//	code := `#include <stdio.h> // 1
//void findMinMax(int arr[], int size, int *min, int *max) { // 2
// // 初始化最小值和最大值为数组的第一个元素 // 3
// *min = arr[0]; // 4
// *max = arr[0]; // 5
// // 遍历数组 // 6
// for (int i = 1; i < size; i++) { // 7
// if (arr[i] > *max) { // 8
// // 发现更大的元素，更新最大值 // 9
// *max = arr[i]; // 10
// } else if (arr[i] < *min) { // 11
// // 发现更小的元素，更新最小值 // 12
// *min = arr[i]; // 13
// } // 14
// } // 15
//} // 16
//int main() { // 17
// int arr[] = {3, 1, 56, 33, 12, 9, 42, 88, 27}; // 18
// int size = sizeof(arr) / sizeof(arr[0]); // 19
// int min, max; // 20
// findMinMax(arr, size, &min, &max); // 21
// printf("Minimum element in array: %d\n", min); // 22
// printf("Maximum element in array: %d\n", max); // 23
// return 0; // 24
//} // 25`
//	if compileFiles, err2 = saveUserCode(constants.LanguageC,
//		code, executePath); err2 != nil {
//		log.Println(err2)
//		return
//	}
//	debugNotificationCallback := func(data interface{}) {
//		cha <- data
//	}
//	debug := NewGdbDebugger()
//	err := debug.Start(&debugger.StartOption{
//		WorkPath:     executePath,
//		CompileFiles: compileFiles,
//		BreakPoints:  []*debugger.Breakpoint{{"/main.c", 22}},
//		GdbCallback:  debugNotificationCallback,
//	})
//	assert.Nil(t, err)
//	// 接受调试编译成功信息
//	data := <-cha
//	assert.Equal(t, &debugger.CompileEvent{
//		Success: true,
//		Message: "用户代码编译成功",
//	}, data)
//	data = <-cha
//	assert.Equal(t, &debugger.LaunchEvent{
//		Success: true,
//		Message: "目标代码加载成功",
//	}, data)
//
//	// 添加断点
//	err = debug.AddBreakpoints([]*debugger.Breakpoint{{"/main.c", 22}})
//	assert.Nil(t, err)
//	data = <-cha
//	assert.Equal(t, &debugger.BreakpointEvent{
//		Reason:      constants.NewType,
//		Breakpoints: []*debugger.Breakpoint{{"/main.c", 22}},
//	}, data)
//
//	<-cha
//	<-cha
//
//	// 读取可视化结构
//	visualizeData, err := debug.VariableVisualize(&debugger.VariableVisualizeQuery{
//		StructVars: []string{"arr"},
//		PointVars:  []string{"i"},
//	})
//	println(visualizeData)
//}
