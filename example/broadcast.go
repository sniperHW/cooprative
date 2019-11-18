package main

import (
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/sniperHW/cooprative"
	"github.com/sniperHW/kendynet"
	codec "github.com/sniperHW/kendynet/example/codec"
	"github.com/sniperHW/kendynet/example/pb"
	"github.com/sniperHW/kendynet/example/testproto"
	connector "github.com/sniperHW/kendynet/socket/connector/tcp"
	listener "github.com/sniperHW/kendynet/socket/listener/tcp"
	"github.com/sniperHW/kendynet/timer"
	"os"
	"strconv"
	"time"
)

func server(service string) {

	packetcount := 0

	clientMap := make(map[kendynet.StreamSession]bool)

	s := cooprative.NewScheduler()

	timer.Repeat(time.Second, nil, func(_ *timer.Timer) {
		s.PostFunc(func() {
			fmt.Printf("clientcount:%d,packetcount:%d\n", len(clientMap), packetcount)
			packetcount = 0
		})
	})

	server, err := listener.New("tcp4", service)
	if server != nil {
		go func() {
			fmt.Printf("server running on:%s\n", service)
			err = server.Serve(func(session kendynet.StreamSession) {
				session.SetEncoder(codec.NewPbEncoder(4096))
				session.SetReceiver(codec.NewPBReceiver(4096))
				session.SetCloseCallBack(func(sess kendynet.StreamSession, reason string) {
					s.PostFunc(func() {
						delete(clientMap, session)
					})
				})
				session.Start(func(ev *kendynet.Event) {
					if ev.EventType == kendynet.EventTypeError {
						session.Close(ev.Data.(error).Error(), 0)
					} else {
						s.PostFunc(func() {
							for s, _ := range clientMap {
								s.Send(ev.Data.(proto.Message))
							}
							packetcount += len(clientMap)
						})
					}
				})

				s.PostFunc(func() {
					clientMap[session] = true
				})
			})

			if nil != err {
				fmt.Printf("TcpServer start failed %s\n", err)
			}
		}()

		s.Start()

	} else {
		fmt.Printf("NewTcpServer failed %s\n", err)
	}
}

func client(service string, count int) {

	client, err := connector.New("tcp4", service)

	if err != nil {
		fmt.Printf("NewTcpClient failed:%s\n", err.Error())
		return
	}

	for i := 0; i < count; i++ {
		session, err := client.Dial(10 * time.Second)
		if err != nil {
			fmt.Printf("Dial error:%s\n", err.Error())
		} else {
			selfID := i + 1
			session.SetEncoder(codec.NewPbEncoder(4096))
			session.SetReceiver(codec.NewPBReceiver(4096))
			session.SetCloseCallBack(func(sess kendynet.StreamSession, reason string) {
				fmt.Printf("client client close:%s\n", reason)
			})
			session.Start(func(event *kendynet.Event) {
				if event.EventType == kendynet.EventTypeError {
					event.Session.Close(event.Data.(error).Error(), 0)
				} else {
					msg := event.Data.(*testproto.BrocastPingpong)
					if msg.GetId() == int64(selfID) {
						event.Session.Send(event.Data.(proto.Message))
					}
				}
			})
			//send the first messge
			o := &testproto.BrocastPingpong{}
			o.Id = proto.Int64(int64(selfID))
			o.Message = proto.String("hello")
			session.Send(o)
		}
	}
}

func main() {
	pb.Register(&testproto.BrocastPingpong{}, 1)
	if len(os.Args) < 3 {
		fmt.Printf("usage ./pingpong [server|client|both] ip:port clientcount\n")
		return
	}

	mode := os.Args[1]

	if !(mode == "server" || mode == "client" || mode == "both") {
		fmt.Printf("usage ./pingpong [server|client|both] ip:port clientcount\n")
		return
	}

	service := os.Args[2]

	sigStop := make(chan bool)

	if mode == "server" || mode == "both" {
		go server(service)
	}

	if mode == "client" || mode == "both" {
		if len(os.Args) < 4 {
			fmt.Printf("usage ./pingpong [server|client|both] ip:port clientcount\n")
			return
		}
		connectioncount, err := strconv.Atoi(os.Args[3])
		if err != nil {
			fmt.Printf(err.Error())
			return
		}
		//让服务器先运行
		time.Sleep(10000000)
		go client(service, connectioncount)

	}

	_, _ = <-sigStop

	return

}
