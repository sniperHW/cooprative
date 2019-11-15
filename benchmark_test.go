package cooprative

import "testing"

import "sync"

var sc *Scheduler = NewScheduler()

func Benchmark1(b *testing.B) {
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

func Benchmark2(b *testing.B) {
	sc.PostFunc(func() {
		sc.Await(func() {})
	})
}
