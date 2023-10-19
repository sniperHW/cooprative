package main

import (
	"fmt"
	"time"

	"github.com/sniperHW/cooprative"
)

type actor struct {
	sc      *cooprative.Scheduler
	mailBox chan func()
	die     chan struct{}
}

func (a *actor) ReentrantCall(other *actor, fn func(chan struct{})) {
	retChan := make(chan struct{})

	other.mailBox <- func() {
		fn(retChan)
	}

	a.sc.Await(func() {
		<-retChan
	})
}

func (a *actor) Call(other *actor, fn func(chan struct{})) {
	retChan := make(chan struct{})
	other.mailBox <- func() {
		fn(retChan)
	}
	<-retChan
}

func (a *actor) Start() {
	go a.sc.Start()
	go func() {
		for {
			select {
			case <-a.die:
				a.sc.Close()
				break
			case fn := <-a.mailBox:
				a.sc.Run(fn)
			}
		}
	}()
}

func main() {
	a := &actor{
		sc:      cooprative.NewScheduler(8),
		mailBox: make(chan func(), 64),
		die:     make(chan struct{}),
	}

	a.Start()

	b := &actor{
		sc:      cooprative.NewScheduler(8),
		mailBox: make(chan func(), 64),
		die:     make(chan struct{}),
	}

	b.Start()

	a.mailBox <- func() {
		fmt.Println("hello")
	}

	a.mailBox <- func() {
		a.ReentrantCall(b, func(retchA chan struct{}) {
			//A调用B，B在处理函数中再次调用A
			b.Call(a, func(retchB chan struct{}) {
				retchB <- struct{}{}
			})
			//ReentrantCall可以成功执行
			fmt.Println("ReentrantCall OK")
		})
	}

	time.Sleep(time.Second)

	a.mailBox <- func() {
		a.Call(b, func(retchA chan struct{}) {
			//A调用B，B在处理函数中再次调用A
			b.Call(a, func(retchB chan struct{}) {
				retchB <- struct{}{}
			})
			//普通Call产生死锁
			fmt.Println("Call OK")
		})
	}

	time.Sleep(time.Second)
}
