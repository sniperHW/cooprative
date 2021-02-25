package main

import (
	"fmt"
	"github.com/sniperHW/cooprative"
	"github.com/sniperHW/kendynet"
	listener "github.com/sniperHW/kendynet/socket/listener/tcp"
	"net/http"
	"os"
)

func main() {

	if len(os.Args) < 2 {
		fmt.Printf("usage ./test_http_get ip:port\n")
		return
	}

	service := os.Args[1]

	s := cooprative.NewScheduler()

	fn := func(session kendynet.StreamSession) {
		var resp *http.Response
		var err error

		ret, _ := s.Await(http.Get, "http://www.01happy.com/demo/accept.php?id=1")

		if nil != ret[0] {
			resp = ret[0].(*http.Response)
		}

		if nil != ret[1] {
			err = ret[1].(error)
		}

		var buff []byte
		var lens int

		if nil != err {
			lens = len(err.Error())
			buff = make([]byte, lens+1)
			copy(buff[:], err.Error()[:lens])
			buff[lens] = byte('\n')
		} else {
			lens = len(resp.Status)
			buff = make([]byte, lens+1)
			copy(buff[:], resp.Status[:lens])
			buff[lens] = byte('\n')
			resp.Body.Close()
		}

		session.SendMessage(kendynet.NewByteBuffer(buff, len(buff)))
		session.Close(nil, 1)
	}

	go func() {
		s.Start()
	}()

	server, err := listener.New("tcp4", service)
	if server != nil {
		fmt.Printf("server running on:%s\n", service)
		err = server.Serve(func(session kendynet.StreamSession) {
			s.Run(fn, session)
		})

		if nil != err {
			fmt.Printf("TcpServer start failed %s\n", err)
		}

	} else {
		fmt.Printf("NewTcpServer failed %s\n", err)
	}

}
