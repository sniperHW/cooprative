package cooprative

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
)

type goroutine struct {
	signal chan *task
	async  bool
}

func (co *goroutine) yield() *task {
	return <-co.signal
}

func (co *goroutine) resume(data *task) {
	co.signal <- data
}

type task struct {
	fn func()
	sc *Scheduler
}

func (t *task) do() {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 65535)
			l := runtime.Stack(buf, false)
			err := fmt.Errorf(fmt.Sprintf("%v: %s", r, buf[:l]))
			log.Println(err)
		}
	}()
	t.fn()
}

type Scheduler struct {
	goroutine
	taskQueue  chan task
	awakeQueue chan *goroutine
	current    *goroutine
	die        chan struct{}
	startOnce  sync.Once
	closeOnce  sync.Once
	awaitCount int32
	closeCh    chan struct{}
}

type SchedulerOption struct {
	TaskQueueCap int //任务队列容量
}

func NewScheduler(opt SchedulerOption) *Scheduler {
	return &Scheduler{
		goroutine: goroutine{
			signal: make(chan *task),
		},
		taskQueue:  make(chan task, opt.TaskQueueCap),
		awakeQueue: make(chan *goroutine, 64),
		die:        make(chan struct{}),
		closeCh:    make(chan struct{}),
	}
}

func (sc *Scheduler) awakeGo(gotine *goroutine) {
	sc.awakeQueue <- gotine
}

func (sc *Scheduler) RunTask(ctx context.Context, fn func()) error {
	select {
	case sc.taskQueue <- task{sc: sc, fn: fn}:
		return nil
	case <-sc.die:
		return errors.New("Scheduler closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (sc *Scheduler) onDie() {
	//处理taskQueue中剩余任务，等待awaitCount变成0
	for atomic.LoadInt32(&sc.awaitCount) > 0 || len(sc.taskQueue) > 0 {
		select {
		case gotine := <-sc.awakeQueue:
			sc.current = gotine
			//唤醒co,然后将自己投入等待，待co将主线程唤醒后继续执行
			gotine.resume(nil)
			atomic.AddInt32(&sc.awaitCount, -1)
			sc.yield()
		case tsk := <-sc.taskQueue:
			sc.runTask(&tsk)
		}
	}
}

const mask int = 0xFFF

type goroutine_pool struct {
	sync.Mutex
	head int
	tail int
	pool [mask + 1]*goroutine
}

func (p *goroutine_pool) put(g *goroutine) bool {
	var ok bool
	p.Lock()
	if (p.tail+1)&mask != p.head {
		p.pool[p.tail] = g
		p.tail = (p.tail + 1) & mask
		ok = true
	}
	p.Unlock()
	return ok
}

func (p *goroutine_pool) get() (g *goroutine) {
	p.Lock()
	if p.head != p.tail {
		g = p.pool[p.head]
		p.head = (p.head + 1) & mask
	}
	p.Unlock()
	return g
}

var gotine_pool goroutine_pool = goroutine_pool{}

func (sc *Scheduler) runTask(tsk *task) {
	gotine := gotine_pool.get()
	if gotine == nil {
		gotine = &goroutine{
			signal: make(chan *task),
		}
		go func() {
			tsk := gotine.yield()
			for {
				tsk.do()
				from := tsk.sc
				if gotine.async || len(from.awakeQueue) > 0 {
					//需要将控制权返回from
					gotine.async = false
					if gotine_pool.put(gotine) {
						from.resume(nil)
						tsk = gotine.yield()
					} else {
						from.resume(nil)
						return
					}
				} else {
					if len(from.taskQueue) > 0 {
						//taskQueue有任务，继续获取任务
						t := <-from.taskQueue
						tsk = &t
					} else {
						//from的任务队列没任务，将控制权返回from
						if gotine_pool.put(gotine) {
							from.resume(nil)
							tsk = gotine.yield()
						} else {
							from.resume(nil)
							return
						}
					}
				}
			}
		}()
	}
	sc.current = gotine
	gotine.resume(tsk)
	sc.yield()
}

func (sc *Scheduler) Start() {
	ok := false
	sc.startOnce.Do(func() { ok = true })
	if ok {
		go func() {
			for {
				select {
				case gotine := <-sc.awakeQueue:
					sc.current = gotine
					//唤醒co,然后将自己投入等待，待co将主线程唤醒后继续执行
					gotine.resume(nil)
					atomic.AddInt32(&sc.awaitCount, -1)
					sc.yield()
				case tsk := <-sc.taskQueue:
					sc.runTask(&tsk)
				case <-sc.die:
					sc.onDie()
					close(sc.closeCh)
					return
				}
			}
		}()
	}
}

func call(fn interface{}, args ...interface{}) (result []interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 65535)
			l := runtime.Stack(buf, false)
			err = fmt.Errorf(fmt.Sprintf("%v: %s", r, buf[:l]))
		}
	}()

	fnType := reflect.TypeOf(fn)
	fnValue := reflect.ValueOf(fn)
	numIn := fnType.NumIn()

	var out []reflect.Value
	if numIn == 0 {
		out = fnValue.Call(nil)
	} else {
		argsLength := len(args)
		argumentIn := numIn
		if fnType.IsVariadic() {
			argumentIn--
		}

		if argsLength < argumentIn {
			panic("with too few input arguments")
		}

		if !fnType.IsVariadic() && argsLength > argumentIn {
			panic("with too many input arguments")
		}

		in := make([]reflect.Value, numIn)
		for i := 0; i < argumentIn; i++ {
			if args[i] == nil {
				in[i] = reflect.Zero(fnType.In(i))
			} else {
				in[i] = reflect.ValueOf(args[i])
			}
		}

		if fnType.IsVariadic() {
			m := argsLength - argumentIn
			slice := reflect.MakeSlice(fnType.In(numIn-1), m, m)
			in[numIn-1] = slice
			for i := 0; i < m; i++ {
				x := args[argumentIn+i]
				if x != nil {
					slice.Index(i).Set(reflect.ValueOf(x))
				}
			}
			out = fnValue.CallSlice(in)
		} else {
			out = fnValue.Call(in)
		}
	}

	if len(out) > 0 {
		result = make([]interface{}, len(out))
		for i, v := range out {
			result[i] = v.Interface()
		}
	}
	return
}

func (sc *Scheduler) Await(fn interface{}, args ...interface{}) (ret []interface{}, err error) {
	atomic.AddInt32(&sc.awaitCount, 1)

	current := sc.current
	current.async = true

	/*  唤醒调度go程，让它可以调度其它任务
	 *  因此function()现在处于并行执行，可以在里面调用线程安全的阻塞或耗时运算
	 */
	sc.resume(nil)

	//并发执行fn
	ret, err = call(fn, args...)

	//将自己添加到待唤醒通道中，然后Wait等待被唤醒后继续执行
	sc.awakeGo(current)
	current.yield()
	return
}

func (sc *Scheduler) Close(waitClose ...bool) {
	ok := false
	sc.closeOnce.Do(func() { ok = true })
	if ok {
		close(sc.die)
	}

	if len(waitClose) > 0 && waitClose[0] {
		<-sc.closeCh
	}
}

var defaultScheduler *Scheduler
var once sync.Once

func Await(fn interface{}, args ...interface{}) ([]interface{}, error) {
	return defaultScheduler.Await(fn, args...)
}

func RunTask(ctx context.Context, fn func()) error {
	once.Do(func() {
		defaultScheduler = NewScheduler(SchedulerOption{TaskQueueCap: 4096})
		go defaultScheduler.Start()
	})

	return defaultScheduler.RunTask(ctx, fn)
}
