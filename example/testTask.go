package main

import (
	"fmt"
	"github.com/sniperHW/cooprative"
	"sync/atomic"
	"time"
)

var (
	c1    int32
	c2    int32
	count int32
)

type Task struct {
	s *cooprative.Scheduler
}

func (this *Task) Do() {
	atomic.AddInt32(&c1, 1)
	atomic.AddInt32(&count, 1)
	c2++
	if c1 != c2 {
		fmt.Printf("not equal,%d,%d\n", c1, c2)
	}

	if c2 >= 1000000 {
		this.s.Close()
		return
	}

	this.s.Await(time.Sleep, time.Millisecond*time.Duration(10))

	this.s.PostTask(this)
}

func main() {

	tickchan := time.Tick(time.Millisecond * time.Duration(1000))
	go func() {
		for {
			_ = <-tickchan
			tmp := atomic.LoadInt32(&count)
			atomic.StoreInt32(&count, 0)
			fmt.Printf("count:%d\n", tmp)

		}
	}()

	s := cooprative.NewScheduler()

	for i := 0; i < 10000; i++ {
		s.PostTask(&Task{
			s: s,
		})
	}

	s.Start()

	fmt.Printf("scheduler stop,total taskCount:%d\n", c2)

}
