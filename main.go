package main

import (
	"bufio"
	"fmt"
	"net"
)

// TCP Server端测试
// 处理函数
func process(conn net.Conn) {
	debugger := NewDebuggerHandler()
	defer conn.Close() // 关闭连接
	for {
		reader := bufio.NewReader(conn)
		var buf [128]byte
		n, err := reader.Read(buf[:])
		if err != nil {
			fmt.Println("read from client failed, err: ", err)
			break
		}
		// 处理请求
		debugger.handle(conn, buf[:n])
	}
}

func main() {
	listen, err := net.Listen("tcp", "127.0.0.1:8888")
	if err != nil {
		fmt.Println("Listen() failed, err: ", err)
		return
	}
	for {
		// 监听客户端的连接请求
		conn, err := listen.Accept()
		if err != nil {
			fmt.Println("Accept() failed, err: ", err)
			continue
		}
		// 启动一个goroutine来处理客户端的连接请求
		go process(conn)
	}
}
