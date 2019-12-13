package main

import (
	"fmt"
	"github.com/sniperHW/cooprative"
	"net"
	"time"
)

//totalRecv的访问在单线程环境下，无需原子操作
var totalRecv int

func onNewClient(s *cooprative.Scheduler, conn net.Conn) {
	fmt.Println("new client")

	recvBuff := make([]byte, 65535/4)
	for !s.IsClosed() {
		ret := s.Await(conn.Read, recvBuff)
		if nil != ret[1] {
			conn.Close()
			fmt.Println("client close")
			break
		}

		totalRecv = totalRecv + ret[0].(int)

		s.Await(conn.Write, recvBuff[:ret[0].(int)])
	}
}

func listen(s *cooprative.Scheduler) {

	tcpAddr, err := net.ResolveTCPAddr("tcp", "localhost:8110")
	if err != nil {
		panic(err.Error())
	}

	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		panic(err.Error())
	}

	for !s.IsClosed() {
		ret := s.Await(listener.Accept)
		conn, err := ret[0].(net.Conn), ret[1]

		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			} else {
				panic(err.(error).Error())
			}

		} else {
			s.PostFunc(onNewClient, s, conn)
		}
	}
}

func main() {
	s := cooprative.NewScheduler()

	s.PostFunc(func() {
		for !s.IsClosed() {
			s.Await(time.Sleep, time.Second)
			fmt.Printf("totalRecv:%dmb\n", totalRecv/1024/1024)
		}
	})

	s.PostFunc(listen, s)

	s.Start()
}
