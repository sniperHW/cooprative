package cooprative

import (
	"reflect"
	"sync"
)

type coroutine struct {
	next   *coroutine
	signal chan interface{}
}

func (this *coroutine) Yild() interface{} {
	return <-this.signal
}

func (this *coroutine) Resume(data interface{}) {
	this.signal <- data
}

func (this *coroutine) Exit() {
	close(this.signal)
}

type coList struct {
	head *coroutine
	tail *coroutine
	size int32
}

func (this *coList) PushFront(element *coroutine) {
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

const (
	type_event = 1
	type_co    = 2
)

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

func (self *eventQueue) pushEvent(event interface{}) error {
	self.guard.Lock()
	ele := &queElement{data: event}
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

func (self *eventQueue) pop() (int, interface{}) {

	var (
		tt   int
		data interface{}
	)

	self.guard.Lock()
	for self.evList.empty() && self.coList.empty() {
		self.cond.Wait()
	}

	if !self.coList.empty() {
		tt = type_co
		data = self.coList.pop().data
	} else {
		tt = type_event
		data = self.evList.pop().data
	}

	self.guard.Unlock()
	return tt, data

}

type Scheduler struct {
	coPool       coList //free coroutine
	queue        *eventQueue
	current      *coroutine //当前正在运行的go程序
	onEvent      func(*Scheduler, interface{})
	selfCo       coroutine
	coCount      int
	reserveCount int
	started      bool
	closed       bool
	mtx          sync.Mutex
}

func NewScheduler(onEvent func(*Scheduler, interface{})) *Scheduler {

	if nil == onEvent {
		panic("nil == onEvent")
	}

	queue := &eventQueue{}
	queue.cond = sync.NewCond(&queue.guard)

	sche := &Scheduler{
		onEvent:      onEvent,
		queue:        queue,
		reserveCount: 10000,
		selfCo:       coroutine{signal: make(chan interface{})},
		coPool:       coList{head: nil, tail: nil, size: 0},
	}
	return sche
}

func (this *Scheduler) yild() {
	this.selfCo.Yild()
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

	out := oriF.Call(in)

	var ret []interface{}

	if len(out) > 0 {
		ret = make([]interface{}, 0, len(out))
		for _, v := range out {
			ret = append(ret, v.Interface())
		}
	}
	//将自己添加到待唤醒通道中，然后Wait等待被唤醒后继续执行
	this.queue.pushCo(co)
	co.Yild()

	return ret

}

func (this *Scheduler) PostEvent(data interface{}) {
	if this.closed {
		return
	}
	this.queue.pushEvent(data)
}

func (this *Scheduler) newCo() {
	for i := 0; i < 10; i++ {
		co := &coroutine{signal: make(chan interface{})}
		this.coPool.PushFront(co)
		go func() {
			for {
				e := co.Yild()
				if nil == e {
					this.coCount--
					this.resume()
					return
				}

				this.onEvent(this, e)

				if this.coCount > this.reserveCount {
					//co数量超过保留大小，终止
					this.coCount--
					this.resume()
					return
				} else {
					this.coPool.PushFront(co)
					this.resume()
				}
			}
		}()
	}
	this.coCount += 10
}

func (this *Scheduler) runTask(e interface{}) {
	for {
		co := this.coPool.Pop()
		if nil == co {
			this.newCo()
		} else {
			//获取一个空闲的go程，用e将其唤醒，然后将自己投入到等待中
			this.current = co
			co.Resume(e)
			this.yild()
			return
		}
	}
}

func (this *Scheduler) Start() {

	this.mtx.Lock()
	if this.closed || this.started {
		this.mtx.Unlock()
		return
	}
	this.started = true
	this.mtx.Unlock()

	for {
		tt, ele := this.queue.pop()
		if tt == type_event {
			if !this.closed {
				this.runTask(ele)
			}
		} else {
			co := ele.(*coroutine)
			this.current = co
			//唤醒co,然后将自己投入等待，待co将主线程唤醒后继续执行
			co.Resume(1)
			this.yild()
		}

		if this.closed {
			for {
				co := this.coPool.Pop()
				if nil != co {
					co.Resume(nil) //发送nil，通告停止
					this.yild()
				} else {
					break
				}
			}
		}

		if this.closed && this.coCount == 0 {
			return
		}

	}
}

func (this *Scheduler) Close() {
	this.mtx.Lock()
	if !this.closed {
		this.closed = true
	}
	this.mtx.Unlock()
}
