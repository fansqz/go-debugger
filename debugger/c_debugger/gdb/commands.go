package gdb

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NotificationCallback is a callback used to report the notifications that GDB
// send asynchronously through the MI2 interface. Responses to the commands are
// not part of these notifications. The notification generic object contains the
// notification sent by GDB.
type NotificationCallback func(notification map[string]interface{})

// AsyncCallback SendAsync方法异步处理命令时的回调函数
type AsyncCallback func(notification map[string]interface{})

// Send issues a command to GDB. Operation is the name of the MI2 command to
// execute without the leading "-" (this means that it is impossible send a CLI
// command), arguments is an optional list of arguments, in GDB parlance the can
// be: options, parameters or "--". It returns a generic object which represents
// the reply of GDB or an error in case the command cannot be delivered to GDB.
func (gdb *Gdb) Send(operation string, arguments ...string) (map[string]interface{}, error) {
	// atomically increase the sequence number and queue a pending command
	pending := make(chan map[string]interface{})
	gdb.mutex.Lock()
	sequence := strconv.FormatInt(gdb.sequence, 10)
	gdb.pending[sequence] = pending
	gdb.sequence++
	gdb.mutex.Unlock()

	// prepare the command
	buffer := bytes.NewBufferString(fmt.Sprintf("%s-%s", sequence, operation))
	for _, argument := range arguments {
		buffer.WriteByte(' ')
		// quote the argument only if needed because GDB interprets un/quoted
		// values differently in some contexts, e.g., when the value is a
		// number('1' vs '"1"') or an option ('--thread' vs '"--thread"')
		if strings.ContainsAny(argument, "\a\b\f\n\r\t\v\\'\" ") {
			argument = strconv.Quote(argument)
		}
		buffer.WriteString(argument)
	}
	buffer.WriteByte('\n')

	// send the command
	if _, err := gdb.stdin.Write(buffer.Bytes()); err != nil {
		return nil, err
	}

	// wait for a response
	result := <-pending
	gdb.mutex.Lock()
	delete(gdb.pending, sequence)
	gdb.mutex.Unlock()
	return result, nil
}

// SendAsync 异步发送，通过回调函数返回执行结果
func (gdb *Gdb) SendAsync(callback AsyncCallback, operation string, arguments ...string) error {
	// atomically increase the sequence number and queue a pending command
	gdb.mutex.Lock()
	sequence := strconv.FormatInt(gdb.sequence, 10)
	gdb.pending2[sequence] = callback
	gdb.sequence++
	gdb.mutex.Unlock()

	// prepare the command
	buffer := bytes.NewBufferString(fmt.Sprintf("%s-%s", sequence, operation))
	for _, argument := range arguments {
		buffer.WriteByte(' ')
		// quote the argument only if needed because GDB interprets un/quoted
		// values differently in some contexts, e.g., when the value is a
		// number('1' vs '"1"') or an option ('--thread' vs '"--thread"')
		if strings.ContainsAny(argument, "\a\b\f\n\r\t\v\\'\" ") {
			argument = strconv.Quote(argument)
		}
		buffer.WriteString(argument)
	}
	buffer.WriteByte('\n')

	// send the command
	if _, err := gdb.stdin.Write(buffer.Bytes()); err != nil {
		return err
	}
	return nil
}

// SendWithTimeout 异步发送请求到dap，带超时
func (gdb *Gdb) SendWithTimeout(timeout time.Duration, operation string, arguments ...string) (map[string]interface{}, error) {
	pending := make(chan map[string]interface{})
	gdb.mutex.Lock()
	sequence := strconv.FormatInt(gdb.sequence, 10)
	gdb.pending[sequence] = pending
	gdb.sequence++
	gdb.mutex.Unlock()
	// prepare the command
	buffer := bytes.NewBufferString(fmt.Sprintf("%s-%s", sequence, operation))
	for _, argument := range arguments {
		buffer.WriteByte(' ')
		if strings.ContainsAny(argument, "\a\b\f\n\r\t\v\\'\" ") {
			argument = strconv.Quote(argument)
		}
		buffer.WriteString(argument)
	}
	buffer.WriteByte('\n')

	// send the command
	if _, err := gdb.stdin.Write(buffer.Bytes()); err != nil {
		return nil, err
	}

	// 启动一个定时器，当超时时间到达时，定时器的通道会接收到信号
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// 同步等待结果或超时
	var result map[string]interface{}
	select {
	case result = <-pending:
		// 收到响应，加锁并从 pending 映射中移除该请求序列号对应的通道
		gdb.mutex.Lock()
		delete(gdb.pending, sequence)
		gdb.mutex.Unlock()
		return result, nil
	case <-timer.C:
		// 超时，加锁并从 pending 映射中移除该请求序列号对应的通道
		gdb.mutex.Lock()
		delete(gdb.pending, sequence)
		gdb.mutex.Unlock()
		return nil, fmt.Errorf("timout: %v", timeout)
	}
}

// CheckedSend works like Send, except that if the result returned by
// gdb has class=error, CheckedSend returns a non-nil error value
// (containing the gdb error message)
func (gdb *Gdb) CheckedSend(operation string, arguments ...string) (map[string]interface{}, error) {
	result, err := gdb.Send(operation, arguments...)

	if err != nil {
		return nil, err
	}

	if result["class"] == "error" {
		if payload, isMap := result["payload"].(map[string]interface{}); isMap {
			if msg, isString := payload["msg"].(string); isString {
				return nil, errors.New(msg)
			}
		}
		// Class is error, but no message? Stringify the entire
		// result as the error message then
		return nil, errors.New("Unknown gdb error: " + fmt.Sprint(result["payload"]))
	}

	return result, nil
}

func (gdb *Gdb) recordReader() {
	scanner := bufio.NewScanner(gdb.stdout)
	for scanner.Scan() {
		// scan the GDB output one line at a time skipping the GDB terminator
		line := scanner.Text()
		if line == terminator {
			continue
		}

		// parse the line and distinguish between command result and
		// notification
		record := parseRecord(line)
		sequence, isResult := record[sequenceKey]
		if isResult {
			// if it is a result record remove the sequence field and complete
			// the pending command
			delete(record, sequenceKey)
			gdb.mutex.RLock()
			if pending, ok := gdb.pending[sequence.(string)]; ok {
				gdb.mutex.RUnlock()
				pending <- record
			} else if callback, ok := gdb.pending2[sequence.(string)]; ok {
				delete(gdb.pending2, sequence.(string))
				gdb.mutex.RUnlock()
				callback(record)
			}
		} else {
			if gdb.onNotification != nil {
				gdb.onNotification(record)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
	gdb.recordReaderDone <- true
}
