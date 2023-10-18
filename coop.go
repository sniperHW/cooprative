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

	if out != nil && len(out) > 0 {
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

func (this *coroutine) Yield() interface{} {
	return <-this.signal
}

func (this *coroutine) Resume(data interface{}) {
	this.signal <- data
}

type task struct {
	fn     interface{}
	params []interface{}
}

func (this *task) do(s *Scheduler) {

	var arguments []interface{}

	fnType := reflect.TypeOf(this.fn)

	if fnType.NumIn() > 0 && fnType.In(0) == reflect.TypeOf(s) {
		arguments = make([]interface{}, len(this.params)+1, len(this.params)+1)
		arguments[0] = s
		copy(arguments[1:], this.params)
	} else {
		arguments = this.params
	}

	ProtectCall(this.fn, arguments...)
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
	closed bool
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

func (this *Scheduler) yield() {
	this.selfCo.Yield()
}

func (this *Scheduler) resume() {
	this.selfCo.Resume(struct{}{})
}

func (this *Scheduler) Await(fn interface{}, args ...interface{}) (ret []interface{}, err error) {
	co := this.current
	/*  唤醒调度go程，让它可以调度其它任务
	 *  因此function()现在处于并行执行，可以在里面调用线程安全的阻塞或耗时运算
	 */
	this.resume()

	ret, err = ProtectCall(fn, args...)

	//将自己添加到待唤醒通道中，然后Wait等待被唤醒后继续执行
	this.queue.pushCo(co)
	co.Yield()
	return
}

func (this *Scheduler) Run(fn interface{}, params ...interface{}) {
	if atomic.LoadInt32(&this.closed) == 0 {
		tt := taskGet()
		tt.fn = fn
		tt.params = params
		this.queue.pushTask(tt)
	}
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
				t.(*task).do(this)
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
	this.startOnce.Do(func() {
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
					var co *coroutine
					this.Lock()
					if this.freeList.Len() > 0 {
						co = this.freeList.Remove(this.freeList.Front()).(*coroutine)
					}
					this.Unlock()
					if nil != co {
						co.Resume(nil) //发送nil，通告停止
						this.yield()
					} else {
						return
					}
				}

				if 0 == atomic.LoadInt32(&this.coCount) {
					return
				}
			}
		}
	})
}

func (this *Scheduler) IsClosed() bool {
	return atomic.LoadInt32(&this.closed) == 1
}

func (this *Scheduler) Close() {
	atomic.CompareAndSwapInt32(&this.closed, 0, 1)
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
