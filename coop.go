package cooprative

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
)

type coroutine struct {
	next   *coroutine
	signal chan interface{}
}

func (this *coroutine) Yield() interface{} {
	return <-this.signal
}

func (this *coroutine) Resume(data interface{}) {
	this.signal <- data
}

type coList struct {
	head *coroutine
	tail *coroutine
	size int32
	mtx  sync.Mutex
}

func (this *coList) PushFront(element *coroutine) {
	this.mtx.Lock()
	defer this.mtx.Unlock()
	element.next = nil
	if this.head == nil && this.tail == nil {
		this.head = element
		this.tail = element
	} else {
		element.next = this.head
		this.head = element
	}
	this.size += 1
}

func (this *coList) Pop() *coroutine {
	this.mtx.Lock()
	defer this.mtx.Unlock()
	if this.head == nil {
		return nil
	} else {
		front := this.head
		this.head = this.head.next
		if this.head == nil {
			this.tail = nil
		}
		this.size -= 1
		return front
	}
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

		in := []reflect.Value{}
		for _, v := range this.params {
			in = append(in, reflect.ValueOf(v))
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
	t.fn = nil //reflect.Zero(reflect.TypeOf(struct{}{}))
	taskPool.Put(t)
}

type queElement struct {
	next *queElement
	data interface{}
}

type que struct {
	head *queElement
	tail *queElement
	size int32
}

func (this *que) push(element *queElement) {
	element.next = nil
	if this.head == nil && this.tail == nil {
		this.head = element
		this.tail = element
	} else {
		this.tail.next = element
		this.tail = element
	}
	this.size += 1
}

func (this *que) pop() *queElement {
	if this.head == nil {
		return nil
	} else {
		front := this.head
		this.head = this.head.next
		if this.head == nil {
			this.tail = nil
		}
		this.size -= 1
		return front
	}
}

func (this *que) empty() bool {
	return this.size == 0
}

/*
 *  event和co使用单独队列，优先返回co队列中的元素，提高co的处理优先级
 */

type eventQueue struct {
	evList que
	coList que
	guard  sync.Mutex
	cond   *sync.Cond
}

func (self *eventQueue) pushTask(t *task) error {
	self.guard.Lock()
	ele := &queElement{data: t}
	self.evList.push(ele)
	self.guard.Unlock()
	self.cond.Signal()
	return nil
}

func (self *eventQueue) pushCo(co *coroutine) error {
	self.guard.Lock()
	ele := &queElement{data: co}
	self.coList.push(ele)
	self.guard.Unlock()
	self.cond.Signal()
	return nil
}

func (self *eventQueue) pop() interface{} {
	defer self.guard.Unlock()
	self.guard.Lock()
	for self.evList.empty() && self.coList.empty() {
		self.cond.Wait()
	}
	if !self.coList.empty() {
		return self.coList.pop().data
	} else {
		return self.evList.pop().data
	}
}

var ReserveCount int32 = 10000

type Scheduler struct {
	coPool  coList //free coroutine
	queue   *eventQueue
	current *coroutine //当前正在运行的go程序
	selfCo  coroutine
	coCount int32
	started int32
	closed  int32
}

func NewScheduler() *Scheduler {

	queue := &eventQueue{}
	queue.cond = sync.NewCond(&queue.guard)

	sche := &Scheduler{
		queue:  queue,
		selfCo: coroutine{signal: make(chan interface{})},
		coPool: coList{head: nil, tail: nil, size: 0},
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
	in := []reflect.Value{}
	for _, v := range args {
		in = append(in, reflect.ValueOf(v))
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

func (this *Scheduler) newCo() {
	for i := 0; i < 10; i++ {
		co := &coroutine{signal: make(chan interface{})}
		this.coPool.PushFront(co)
		atomic.AddInt32(&this.coCount, 1)
		go func() {
			defer func() {
				atomic.AddInt32(&this.coCount, -1)
				this.resume()
			}()
			for t := co.Yield(); t != nil; t = co.Yield() {
				t.(*task).do()
				taskPut(t.(*task))
				if 1 == atomic.LoadInt32(&this.closed) || atomic.LoadInt32(&this.coCount) > ReserveCount {
					break
				} else {
					this.coPool.PushFront(co)
					this.resume()
				}
			}
		}()
	}
}

func (this *Scheduler) runTask(t *task) {
	for {
		co := this.coPool.Pop()
		if nil == co {
			this.newCo()
		} else {
			//获取一个空闲的go程，用e将其唤醒，然后将自己投入到等待中
			this.current = co
			co.Resume(t)
			this.yield()
			return
		}
	}
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
				co := this.coPool.Pop()
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
