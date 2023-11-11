package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sniperHW/cooprative"
)

type actor struct {
	mailBox *cooprative.Scheduler
}

func (a *actor) ReentrantCall(other *actor, fn func(chan struct{})) {
	retChan := make(chan struct{})
	other.OnEvent(func() { fn(retChan) })
	a.mailBox.Await(func() { <-retChan })
}

func (a *actor) Call(other *actor, fn func(chan struct{})) {
	retChan := make(chan struct{})
	other.OnEvent(func() { fn(retChan) })
	<-retChan
}

func (a *actor) Start() {
	a.mailBox.Start()
}

func (a *actor) OnEvent(fn func()) {
	a.mailBox.RunTask(context.Background(), fn)
}

func main() {
	a := &actor{mailBox: cooprative.NewScheduler(cooprative.SchedulerOption{TaskQueueCap: 64})}

	a.Start()

	b := &actor{mailBox: cooprative.NewScheduler(cooprative.SchedulerOption{TaskQueueCap: 64})}

	b.Start()

	a.OnEvent(func() {
		fmt.Println("in A,before a.ReentrantCall")
		a.ReentrantCall(b, func(retchA chan struct{}) {
			fmt.Println("in B,before b.Call")
			//A调用B，B在处理函数中再次调用A
			b.Call(a, func(retchB chan struct{}) {
				fmt.Println("in A")
				retchB <- struct{}{}
			})
			//ReentrantCall可以成功执行
			fmt.Println("b.Call OK")
			retchA <- struct{}{}
		})
		fmt.Println("a.ReentrantCall OK")
	})

	time.Sleep(time.Second)

	a.OnEvent(func() {
		fmt.Println("in A,before a.Call")
		a.Call(b, func(retchA chan struct{}) {
			fmt.Println("in B,before b.Call")
			//A调用B，B在处理函数中再次调用A，此时A阻塞在a.Call上,b.Call投递到a.Mailbox中的func无法被执行
			b.Call(a, func(retchB chan struct{}) {
				fmt.Println("in A")
				retchB <- struct{}{}
			})
			//普通Call产生死锁
			fmt.Println("b.Call OK")
			retchA <- struct{}{}
		})
		fmt.Println("a.Call OK")
	})

	time.Sleep(time.Second)
}
