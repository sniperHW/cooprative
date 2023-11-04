package cooprative

import (
	//"fmt"
	"context"
	"sync"
	"testing"
)

func Benchmark2(b *testing.B) {

	var sc *Scheduler = NewScheduler(SchedulerOption{TaskQueueCap: 4096})

	go func() {
		sc.Start()
	}()

	for i := 0; i < b.N; i++ {
		die := make(chan struct{})
		sc.RunTask(context.Background(), func() {
			sc.Await(func() {
			})
			close(die)
		})
		<-die
	}

	sc.Close()
}

func Benchmark3(b *testing.B) {

	var sc *Scheduler = NewScheduler(SchedulerOption{TaskQueueCap: 4096})

	go func() {
		sc.Start()
	}()

	var wait sync.WaitGroup

	for i := 0; i < b.N; i++ {
		wait.Add(1)
		sc.RunTask(context.Background(), func() {
			sc.Await(func() {
			})
			wait.Done()
		})
	}

	wait.Wait()

	sc.Close()
}
