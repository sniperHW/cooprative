package main

import (
	"fmt"
	"github.com/sniperHW/cooprative"
	"net"
	"time"
)

//totalRecv的访问在单线程环境下，无需原子操作
var totalRecv int

func onNewClient(conn net.Conn) {
	fmt.Println("new client")

	recvBuff := make([]byte, 65535/4)
	for {
		ret, _ := cooprative.Await(conn.Read, recvBuff)
		if nil != ret[1] {
			conn.Close()
			fmt.Println("client close")
			break
		}

		totalRecv = totalRecv + ret[0].(int)

		cooprative.Await(conn.Write, recvBuff[:ret[0].(int)])
	}
}

func listen() {

	tcpAddr, err := net.ResolveTCPAddr("tcp", "localhost:8110")
	if err != nil {
		panic(err.Error())
	}

	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		panic(err.Error())
	}

	for {
		ret := cooprative.Await(listener.Accept)
		conn, err := ret[0].(net.Conn), ret[1]

		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			} else {
				panic(err.(error).Error())
			}

		} else {
			cooprative.Run(onNewClient, conn)
		}
	}
}

func main() {

	cooprative.Run(func() {
		for {
			cooprative.Await(time.Sleep, time.Second)
			fmt.Printf("totalRecv:%dmb\n", totalRecv/1024/1024)
		}
	})

	cooprative.Run(listen)

	sigStop := make(chan bool)
	_, _ = <-sigStop

}
