package cooprative

import (
	"container/list"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
)

type coroutine struct {
	signal chan interface{}
}

func (this *coroutine) Yield() interface{} {
	return <-this.signal
}

func (this *coroutine) Resume(data interface{}) {
	this.signal <- data
}

type TaskI interface {
	Do()
}

type task struct {
	taskI  TaskI
	fn     *reflect.Value
	params []interface{}
}

func (this *task) do() (err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 65535)
			l := runtime.Stack(buf, false)
			err = fmt.Errorf(fmt.Sprintf("%v: %s", r, buf[:l]))
			fmt.Println(err.Error())
		}
	}()

	if nil != this.taskI {
		this.taskI.Do()
	} else {

		fnType := (*this.fn).Type()
		var in []reflect.Value
		numIn := fnType.NumIn()
		if numIn > 0 {
			in = make([]reflect.Value, numIn)
			for i := 0; i < numIn; i++ {
				if i >= len(this.params) || this.params[i] == nil {
					in[i] = reflect.Zero(fnType.In(i))
				} else {
					in[i] = reflect.ValueOf(this.params[i])
				}
			}
		}

		this.fn.Call(in)
	}
	return
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
	t.taskI = nil
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
}

func (self *eventQueue) pushTask(t *task) error {
	self.guard.Lock()
	self.evList.PushBack(t)
	self.guard.Unlock()
	self.cond.Signal()
	return nil
}

func (self *eventQueue) pushCo(co *coroutine) error {
	self.guard.Lock()
	self.coList.PushBack(co)
	self.guard.Unlock()
	self.cond.Signal()
	return nil
}

func (self *eventQueue) pop() interface{} {
	defer self.guard.Unlock()
	self.guard.Lock()

	for self.evList.Len() == 0 && self.coList.Len() == 0 {
		self.cond.Wait()
	}

	if self.coList.Len() > 0 {
		return self.coList.Remove(self.coList.Front())
	} else {
		return self.evList.Remove(self.evList.Front())
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
	started      int32
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

func (this *Scheduler) yield() {
	this.selfCo.Yield()
}

func (this *Scheduler) resume() {
	this.selfCo.Resume(struct{}{})
}

func (this *Scheduler) Await(fn interface{}, args ...interface{}) []interface{} {
	oriF := reflect.ValueOf(fn)

	if oriF.Kind() != reflect.Func {
		panic("fn is not a func")
	}

	fnType := reflect.TypeOf(fn)

	var in []reflect.Value
	numIn := fnType.NumIn()
	if numIn > 0 {
		in = make([]reflect.Value, numIn)
		for i := 0; i < numIn; i++ {
			if i >= len(args) || args[i] == nil {
				in[i] = reflect.Zero(fnType.In(i))
			} else {
				in[i] = reflect.ValueOf(args[i])
			}
		}
	}

	co := this.current
	/* 唤醒调度go程，让它可以调度其它任务
	*  因此function()现在处于并行执行，可以在里面调用线程安全的阻塞或耗时运算
	 */
	this.resume()

	var ret []interface{}

	func() {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 65535)
				l := runtime.Stack(buf, false)
				err := fmt.Errorf(fmt.Sprintf("%v: %s", r, buf[:l]))
				fmt.Println(err.Error())
			}
		}()

		out := oriF.Call(in)

		if len(out) > 0 {
			ret = make([]interface{}, 0, len(out))
			for _, v := range out {
				ret = append(ret, v.Interface())
			}
		}
	}()

	//将自己添加到待唤醒通道中，然后Wait等待被唤醒后继续执行
	this.queue.pushCo(co)
	co.Yield()

	return ret

}

func (this *Scheduler) PostTask(t TaskI) {
	if atomic.LoadInt32(&this.closed) == 1 {
		return
	}

	tt := taskGet()
	tt.taskI = t
	this.queue.pushTask(tt)
}

func (this *Scheduler) PostFunc(fn interface{}, params ...interface{}) {
	if atomic.LoadInt32(&this.closed) == 1 {
		return
	}

	fnV := reflect.ValueOf(fn)

	if fnV.Kind() != reflect.Func {
		panic("fn is not a func")
	}

	tt := taskGet()
	tt.fn = &fnV
	tt.params = params
	this.queue.pushTask(tt)
}

func (this *Scheduler) getFree() *coroutine {
	this.Lock()
	defer this.Unlock()
	if this.freeList.Len() != 0 {
		return this.freeList.Remove(this.freeList.Front()).(*coroutine)
	} else {
		co := &coroutine{signal: make(chan interface{})}
		atomic.AddInt32(&this.coCount, 1)
		go func() {
			defer func() {
				atomic.AddInt32(&this.coCount, -1)
				this.resume()
			}()
			for t := co.Yield(); t != nil; t = co.Yield() {
				t.(*task).do()
				taskPut(t.(*task))
				if 1 == atomic.LoadInt32(&this.closed) || atomic.LoadInt32(&this.coCount) > this.reserveCount {
					break
				} else {
					this.putFree(co)
					this.resume()
				}
			}
		}()
		return co
	}
}

func (this *Scheduler) putFree(co *coroutine) {
	this.Lock()
	defer this.Unlock()
	this.freeList.PushFront(co)
}

func (this *Scheduler) runTask(t *task) {
	co := this.getFree()
	this.current = co
	co.Resume(t)
	this.yield()
	return
}

func (this *Scheduler) Start() {

	if !atomic.CompareAndSwapInt32(&this.started, 0, 1) {
		return
	}

	for {
		ele := this.queue.pop()
		switch ele.(type) {
		case *coroutine:
			co := ele.(*coroutine)
			this.current = co
			//唤醒co,然后将自己投入等待，待co将主线程唤醒后继续执行
			co.Resume(struct{}{})
			this.yield()
			break
		case *task:
			if 0 == atomic.LoadInt32(&this.closed) {
				this.runTask(ele.(*task))
			}
			break
		}

		if 1 == atomic.LoadInt32(&this.closed) {

			for {
				co := func() *coroutine {
					this.Lock()
					defer this.Unlock()
					if this.freeList.Len() == 0 {
						return nil
					} else {
						return this.freeList.Remove(this.freeList.Front()).(*coroutine)
					}
				}()

				if nil != co {
					co.Resume(nil) //发送nil，通告停止
					this.yield()
				} else {
					break
				}
			}

			if 0 == atomic.LoadInt32(&this.coCount) {
				break
			}
		}
	}
}

func (this *Scheduler) IsClosed() bool {
	return atomic.LoadInt32(&this.closed) == 1
}

func (this *Scheduler) Close() {
	atomic.CompareAndSwapInt32(&this.closed, 0, 1)
}

var defaultScheduler *Scheduler
var once sync.Once

func IsClosed() bool {
	return defaultScheduler.IsClosed()
}

func Close() {
	defaultScheduler.Close()
}

func Await(fn interface{}, args ...interface{}) []interface{} {
	once.Do(func() {
		defaultScheduler = NewScheduler()
		go func() {
			defaultScheduler.Start()
		}()
	})
	return defaultScheduler.Await(fn, args...)
}

func PostTask(t TaskI) {
	once.Do(func() {
		defaultScheduler = NewScheduler()
		go func() {
			defaultScheduler.Start()
		}()
	})

	defaultScheduler.PostTask(t)
}

func PostFunc(fn interface{}, params ...interface{}) {
	once.Do(func() {
		defaultScheduler = NewScheduler()
		go func() {
			defaultScheduler.Start()
		}()
	})

	defaultScheduler.PostFunc(fn, params...)
}
