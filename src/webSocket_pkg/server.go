package main

import (
	"net/http"
	"time"
	"webSocket_pkg/impl"

	"github.com/gorilla/websocket"
)

/*
封装WebSocket
	缺乏工程化的设计
		其他代码模块, 无法直接操作WebSocket连接
		WebSocket连接非线程安全, 并发读/写需要同步手段

	隐藏细节, 封装API
		封装Connection结构, 隐藏WebSocket底层连接
		封装Connection的API, 提供Send, Read, Close 等线程安全接口

	API原理
		SendMessage将消息投递到out channel
		ReadMessage从in channel读取消息

	内部原理
		启动读协程, 循环读取WebSocket, 将消息投递到in channel
		启动写协程, 循环读取out channel, 将消息写给Websocket

分析技术难点
	内核瓶颈
		推送量大: 100w在线 * 10条/s = 1000w条/s
		内核瓶颈: linux内核发送TCP的极限包频 ~ 100w

	锁瓶颈
		需要维护在线用户集合(100w在线), 通常是一个字典结构
		推送消息即遍历整个集合, 顺序发送消息, 耗时极长
		推送期间, 客户端仍旧正常上/下线, 所以集合需要加锁

	CPU瓶颈
		浏览器与服务端通常采取json格式通讯
		json编码非常耗费cpu资源
		向100w在线推送1次, 则需要100w次json encode

技术难点的解决方案
	内核瓶颈
		优化原理: 减少网络小包(对内核, 传输的网络设备会造成处理压力)的发送
		优化方案:
			将同一秒内的N条消息, 合并成1条消息
			合并后, 每秒推送次数只等于在线连接数
	锁瓶颈
		优化原理: (锁)大拆小
		优化方案:
			连接打散到多个集合中, 每个集合有自己的锁
			多线程并发推送多个集合, 避免锁竞争
			读写锁(该锁可以被同时多个读取者持有或唯一个写入者持有)取代互斥锁, 多个推送任务可以并发遍历相同集合

	cpu瓶颈
		优化原理: 减少重复计算
		优化方案:
			json编码前置, 1次消息编码 + 100w次推送
			消息合并前置, N条消息合并后只编码一次

揭秘分布式架构
单机瓶颈
	维护海量长连接会花费不少内存
	消息推送瞬时消耗大量CPU资源
	消息推送瞬时带宽高达400~600MB(4-6Gbits)(万兆网卡), 是主要瓶颈(一般网卡只能跑到100MB(千兆网卡))
分布式架构
	网关层的横向扩展: 将网关做成集群, 部署多个节点, 前面做个负载均衡, 连接被打散到多个服务器中.
					->导致推送消息时, 不知道直播间在哪个网关节点中. ->解决方案: 将消息广播到所有网关节点中,
					各个网关节点自行判断, 自行进行推送

	网关层广播: 逻辑集群
		基于HTTP/2协议向gateway集群分发消息
			http/2支持连接复用, 用作RPC(高性能内部通讯)性能更佳
		基于HTTP/1协议对外提供推送API
			http/1更加普及, 对业务方更加友好
*/
var (
	upgrader = websocket.Upgrader{
		// 允许跨域
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

func wsHandler(w http.ResponseWriter, r *http.Request) {
	var (
		wsConn *websocket.Conn
		err    error
		conn   *impl.Connection
		// messageType int
		data []byte
	)
	// http 的应答, 建立socket连接, 响应
	// Upgrade: websocket
	if wsConn, err = upgrader.Upgrade(w, r, nil); err != nil {
		return
	}

	if conn, err = impl.InitConnection(wsConn); err != nil {
		goto ERR
	}
	// 线程安全
	go func() {
		var (
			err error
		)
		for {
			// 假使发送消息的时候, 连接出错, 则推出
			if err = conn.WriteMessage([]byte("heartbeat")); err != nil {
				return
			}
			time.Sleep(1 * time.Second)
		}
	}()
	// websocket.Conn
	// websocket传递的数据类型: Text, Binary
	for {
		// 读取消息
		if data, err = conn.ReadMessage(); err != nil {
			goto ERR
		}
		// if err = conn.WriteMessage(messageType, data); err != nil {
		// 返回消息
		if err = conn.WriteMessage(data); err != nil {
			goto ERR
		}
	}
ERR:
	conn.Close()
}

func main() {
	http.HandleFunc("/ws", wsHandler)

	http.ListenAndServe(":7777", nil)
}
