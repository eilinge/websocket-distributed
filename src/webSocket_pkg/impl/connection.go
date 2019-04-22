package impl

import (
	"errors"
	"sync"

	"github.com/gorilla/websocket"
)

// Connection :线程安全的wsConnect
type Connection struct {
	wsConn    *websocket.Conn
	inChan    chan []byte
	outChan   chan []byte
	closeChan chan byte // 防止读取(传输)中断后, 阻塞
	isClosed  bool
	mutex     sync.Mutex
}

// InitConnection :init conn and start conn.readloop/conn.writeloop
func InitConnection(wsConn *websocket.Conn) (conn *Connection, err error) {
	conn = &Connection{
		wsConn:    wsConn,
		inChan:    make(chan []byte, 1000),
		outChan:   make(chan []byte, 1000),
		closeChan: make(chan byte, 1),
	}
	go conn.readloop()
	go conn.writeloop()
	return
}

// ReadMessage :外部可调用
func (conn *Connection) ReadMessage() (data []byte, err error) {
	// 假使用户进行读取(传输)消息的时候, 连接出错了, 用户也要进行退出调用该API
	select {
	case data = <-conn.inChan:
	case <-conn.closeChan:
		err = errors.New("conncet is closed")
	}

	return
}

// WriteMessage :外部可调用
func (conn *Connection) WriteMessage(data []byte) (err error) {
	select {
	case conn.outChan <- data:
	case <-conn.closeChan:
		err = errors.New("connection is closed")
	}

	return
}

// Close :外部可调用
func (conn *Connection) Close() {
	// 线程安全, 可重入(多次调用)的Close
	conn.wsConn.Close()

	// 保证线程安全(进行加锁), closeChan只关闭一次(状态位)
	conn.mutex.Lock()
	if !conn.isClosed {
		close(conn.closeChan)
		conn.isClosed = true
	}
	conn.mutex.Unlock()

}

// readloop :内部实现
func (conn *Connection) readloop() {
	var (
		data []byte
		err  error
	)
	for {
		if _, data, err = conn.wsConn.ReadMessage(); err != nil {
			goto ERR
		}
		// 假使读取(发送)循环报错, 长连接关闭, closeChan关闭, 直接关闭长连接, 程序退出
		select {
		case conn.inChan <- data:
		case <-conn.closeChan:
			// closeChan进行关闭
			goto ERR
		}

	}

ERR:
	conn.Close()
}

func (conn *Connection) writeloop() {
	var (
		data []byte
		err  error
	)
	for {

		select {
		case data = <-conn.outChan:
		case <-conn.closeChan:
			goto ERR
		}

		if err = conn.wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
			goto ERR
		}
	}
ERR:
	conn.Close()
}
