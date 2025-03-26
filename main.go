package main

import (
	"flag"
	"github.com/fansqz/go-debugger/debugger"
	"github.com/fansqz/go-debugger/debugger/c_debugger"
	"github.com/google/go-dap"
	"log"
	"net"
)

var ConnList []net.Conn

// 定义版本号
const Version = "1.0.0"

func main() {
	showVersion := flag.Bool("version", false, "Show the version number")
	port := flag.String("port", "5000", "TCP port to listen on")
	execFile := flag.String("file", "", "Exec file")
	language := flag.String("language", "c", "Program language")
	flag.Parse()

	// 检查是否需要显示版本信息
	if *showVersion {
		log.Printf("Version: %s\n", Version)
		return
	}
	if execFile == nil || *execFile == "" {
		log.Fatal("exec file cannot be empty")
		return
	}
	if language == nil || *language == "" {
		log.Fatal("language cannot be empty")
		return
	}
	// 监听端口
	listener, err := net.Listen("tcp", ":"+*port)
	if err != nil {
		log.Fatalf("failed to start listening on the port: %s\n", *port)
		return
	}
	defer listener.Close()
	log.Println("Started server at", listener.Addr())

	// 启动调试器
	debug, err := createDebugger(*language, *execFile)
	if err != nil {
		log.Fatalf("start debug fail, err = %s\n", err)
		return
	}

	for {
		conn, err := listener.Accept()
		ConnList = append(ConnList, conn)
		if err != nil {
			log.Println("Connection failed:", err)
			continue
		}
		log.Println("Accepted connection from", conn.RemoteAddr())
		// Handle multiple client connections concurrently
		go handleConnection(conn, debug)
	}
}

// createDebugger 创建调试器
func createDebugger(language string, execFile string) (debugger.Debugger, error) {
	var d debugger.Debugger
	switch language {
	case "c":
		d = c_debugger.NewCDebugger()
	}
	err := d.Start(&debugger.StartOption{
		ExecFile: execFile,
		Callback: func(event dap.EventMessage) {
			for _, conn := range ConnList {
				dap.WriteProtocolMessage(conn, event)
			}
		},
	})
	return d, err
}
