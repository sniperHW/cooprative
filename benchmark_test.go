package cooprative

import (
	//"fmt"
	"sync"
	"testing"
)

//var sc *Scheduler = NewScheduler()

//func init() {
//	sc.Start()
//}

func Benchmark1(b *testing.B) {
	for i := 0; i < b.N; i++ {

		var wait sync.WaitGroup

		co1 := &coroutine{signal: make(chan interface{})}
		co2 := &coroutine{signal: make(chan interface{})}

		wait.Add(1)
		wait.Add(1)

		go func() {
			co1.Yield()
			co2.Resume(nil)
			wait.Done()
		}()

		go func() {
			co1.Resume(nil)
			co2.Yield()
			wait.Done()
		}()

		wait.Wait()
	}
}

func Benchmark2(b *testing.B) {

	var sc *Scheduler = NewScheduler()

	go func() {
		sc.Start()
	}()

	for i := 0; i < b.N; i++ {
		die := make(chan struct{})
		sc.PostFunc(func() {
			sc.Await(func() {
			})
			close(die)
		})
		<-die
	}

	sc.Close()
}

func Benchmark3(b *testing.B) {

	var sc *Scheduler = NewScheduler()

	go func() {
		sc.Start()
	}()

	var wait sync.WaitGroup

	for i := 0; i < b.N; i++ {
		wait.Add(1)
		sc.PostFunc(func() {
			sc.Await(func() {
			})
			wait.Done()
		})
	}

	wait.Wait()

	sc.Close()
}
