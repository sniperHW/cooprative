package cooprative

import (
	"container/list"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
)

func Call(fn interface{}, args ...interface{}) (result []interface{}) {
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

		/*if !fnType.IsVariadic() && argsLength > argumentIn {
			panic("ProtectCall with too many input arguments")
		}*/

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

func ProtectCall(fn interface{}, args ...interface{}) (result []interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 65535)
			l := runtime.Stack(buf, false)
			err = fmt.Errorf(fmt.Sprintf("%v: %s", r, buf[:l]))
		}
	}()
	result = Call(fn, args...)
	return
}

type coroutine struct {
	signal chan interface{}
}

func (co *coroutine) Yield() interface{} {
	return <-co.signal
}

func (co *coroutine) Resume(data interface{}) {
	co.signal <- data
}

type task struct {
	fn     interface{}
	params []interface{}
}

func (t *task) do(s *Scheduler) {

	var arguments []interface{}

	fnType := reflect.TypeOf(t.fn)

	if fnType.NumIn() > 0 && fnType.In(0) == reflect.TypeOf(s) {
		arguments = make([]interface{}, len(t.params)+1)
		arguments[0] = s
		copy(arguments[1:], t.params)
	} else {
		arguments = t.params
	}

	ProtectCall(t.fn, arguments...)
}

var taskPool = sync.Pool{
	New: func() interface{} {
		return &task{}
	},
}

func taskGet() *task {
	r := taskPool.Get().(*task)
	return r
}

func taskPut(t *task) {
	t.params = nil
	t.fn = nil
	taskPool.Put(t)
}

/*
 *  event和co使用单独队列，优先返回co队列中的元素，提高co的处理优先级
 */

type eventQueue struct {
	evList *list.List
	coList *list.List
	guard  sync.Mutex
	cond   *sync.Cond
	//closed bool
}

func (q *eventQueue) pushTask(t *task) error {
	q.guard.Lock()
	q.evList.PushBack(t)
	q.guard.Unlock()
	q.cond.Signal()
	return nil
}

func (q *eventQueue) pushCo(co *coroutine) error {
	q.guard.Lock()
	q.coList.PushBack(co)
	q.guard.Unlock()
	q.cond.Signal()
	return nil
}

func (q *eventQueue) pop() interface{} {
	defer q.guard.Unlock()
	q.guard.Lock()

	for q.evList.Len() == 0 && q.coList.Len() == 0 {
		q.cond.Wait()
	}

	if q.coList.Len() > 0 {
		return q.coList.Remove(q.coList.Front())
	} else {
		return q.evList.Remove(q.evList.Front())
	}
}

type Scheduler struct {
	sync.Mutex
	queue        *eventQueue
	current      *coroutine //当前正在运行的go程序
	selfCo       coroutine
	coCount      int32
	reserveCount int32
	freeList     *list.List
	startOnce    sync.Once
	closed       int32
}

func NewScheduler(reserveCount ...int32) *Scheduler {

	queue := &eventQueue{
		evList: list.New(),
		coList: list.New(),
	}
	queue.cond = sync.NewCond(&queue.guard)

	sche := &Scheduler{
		queue:        queue,
		selfCo:       coroutine{signal: make(chan interface{})},
		reserveCount: 10000,
		freeList:     list.New(),
	}

	if len(reserveCount) > 0 {
		sche.reserveCount = reserveCount[0]
	}

	return sche
}

func (sc *Scheduler) yield() {
	sc.selfCo.Yield()
}

func (sc *Scheduler) resume() {
	sc.selfCo.Resume(struct{}{})
}

func (sc *Scheduler) Await(fn interface{}, args ...interface{}) (ret []interface{}, err error) {
	co := sc.current
	/*  唤醒调度go程，让它可以调度其它任务
	 *  因此function()现在处于并行执行，可以在里面调用线程安全的阻塞或耗时运算
	 */
	sc.resume()

	ret, err = ProtectCall(fn, args...)

	//将自己添加到待唤醒通道中，然后Wait等待被唤醒后继续执行
	sc.queue.pushCo(co)
	co.Yield()
	return
}

func (sc *Scheduler) Run(fn interface{}, params ...interface{}) {
	if atomic.LoadInt32(&sc.closed) == 0 {
		tt := taskGet()
		tt.fn = fn
		tt.params = params
		sc.queue.pushTask(tt)
	}
}

func (sc *Scheduler) getFree() *coroutine {
	sc.Lock()
	defer sc.Unlock()
	if sc.freeList.Len() != 0 {
		return sc.freeList.Remove(sc.freeList.Front()).(*coroutine)
	} else {
		co := &coroutine{signal: make(chan interface{})}
		atomic.AddInt32(&sc.coCount, 1)
		go func() {
			defer func() {
				atomic.AddInt32(&sc.coCount, -1)
				sc.resume()
			}()
			for t := co.Yield(); t != nil; t = co.Yield() {
				t.(*task).do(sc)
				taskPut(t.(*task))
				if 1 == atomic.LoadInt32(&sc.closed) || atomic.LoadInt32(&sc.coCount) > sc.reserveCount {
					break
				} else {
					sc.putFree(co)
					sc.resume()
				}
			}
		}()
		return co
	}
}

func (sc *Scheduler) putFree(co *coroutine) {
	sc.Lock()
	defer sc.Unlock()
	sc.freeList.PushFront(co)
}

func (sc *Scheduler) runTask(t *task) {
	co := sc.getFree()
	sc.current = co
	co.Resume(t)
	sc.yield()
	return
}

func (sc *Scheduler) Start() {
	sc.startOnce.Do(func() {
		for {
			ele := sc.queue.pop()
			switch o := ele.(type) {
			case *coroutine:
				sc.current = o
				//唤醒co,然后将自己投入等待，待co将主线程唤醒后继续执行
				o.Resume(struct{}{})
				sc.yield()
			case *task:
				if atomic.LoadInt32(&sc.closed) == 0 {
					sc.runTask(o)
				}
			}

			if atomic.LoadInt32(&sc.closed) == 1 {
				for {
					var co *coroutine
					sc.Lock()
					if sc.freeList.Len() > 0 {
						co = sc.freeList.Remove(sc.freeList.Front()).(*coroutine)
					}
					sc.Unlock()
					if nil != co {
						co.Resume(nil) //发送nil，通告停止
						sc.yield()
					} else {
						return
					}
				}
			}
		}
	})
}

func (sc *Scheduler) IsClosed() bool {
	return atomic.LoadInt32(&sc.closed) == 1
}

func (sc *Scheduler) Close() {
	atomic.CompareAndSwapInt32(&sc.closed, 0, 1)
}

var defaultScheduler *Scheduler
var once sync.Once

func Await(fn interface{}, args ...interface{}) ([]interface{}, error) {
	return defaultScheduler.Await(fn, args...)
}

func Run(fn interface{}, params ...interface{}) {
	once.Do(func() {
		defaultScheduler = NewScheduler()
		go func() {
			defaultScheduler.Start()
		}()
	})

	defaultScheduler.Run(fn, params...)
}
