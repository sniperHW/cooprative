package main

import (
	"fmt"
	"github.com/sniperHW/cooprative"
	"runtime"
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

	for !this.s.IsClosed() {
		c1++
		count++
		c2++
		if c1 != c2 {
			fmt.Printf("not equal,%d,%d\n", c1, c2)
		}

		if c2 >= 5000000 {
			this.s.Close()
			return
		}

		this.s.Await(runtime.Gosched)
	}
}

func main() {

	s := cooprative.NewScheduler()

	s.PostFunc(func() {
		for !s.IsClosed() {
			s.Await(time.Sleep, time.Second)
			fmt.Printf("count:%d\n", count)
			count = 0
		}
	})

	for i := 0; i < 10000; i++ {
		s.PostTask(&Task{
			s: s,
		})
	}

	s.Start()

	fmt.Printf("scheduler stop,total taskCount:%d\n", c2)

}
