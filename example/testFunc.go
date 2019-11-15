package main

import (
	"fmt"
	"github.com/sniperHW/cooprative"
	"time"
)

func main() {

	c1 := int32(0)
	c2 := int32(0)
	count := int32(0)

	s := cooprative.NewScheduler()

	fn := func() {
		for !s.IsClosed() {
			c1++
			count++
			c2++
			if c1 != c2 {
				fmt.Printf("not equal,%d,%d\n", c1, c2)
			}

			if c2 >= 5000000 {
				s.Close()
				return
			}

			s.Await(time.Sleep, time.Millisecond*time.Duration(10))
		}
	}

	s.PostFunc(func() {
		for !s.IsClosed() {
			s.Await(time.Sleep, time.Second)
			fmt.Printf("count:%d\n", count)
			count = 0
		}
	})

	for i := 0; i < 10000; i++ {
		s.PostFunc(fn)
	}

	s.Start()

	fmt.Printf("scheduler stop,total taskCount:%d\n", c2)

}
