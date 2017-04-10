package coop

import(
	"sync"
	"fmt"
)

type coroutine struct {
	next  *coroutine
	que    chan interface{}
}

func (this *coroutine) Yild() interface{} {
	return <- this.que
}

func (this *coroutine) Resume(data interface{}) {
	this.que <- data
}

func (this *coroutine) Exit() {
	close(this.que)
}


type coList struct{
	head * coroutine
	tail * coroutine
	size int32
}

func (this *coList) PushFront(element *coroutine) {
	element.next = nil
	if this.head == nil && this.tail == nil {
		this.head = element
		this.tail = element
	}else{
		element.next = this.head
		this.head = element
	}
	this.size += 1	
}

func (this *coList) Pop() (*coroutine){
	if this.head == nil {
		return nil
	}else{
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
	type_event  = 1
	type_co     = 2
)

type queElement struct {
	next       *queElement
	data        interface {}
}

type que struct {
	head *queElement
	tail *queElement
	size  int32	
}

func (this *que) push(element *queElement) {
	element.next = nil
	if this.head == nil && this.tail == nil {
		this.head = element
		this.tail = element
	}else{
		this.tail.next = element
		this.tail = element
	}
	this.size += 1
}

func (this *que) pop() (*queElement){
	if this.head == nil {
		return nil
	}else{
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
	evList     que
	coList     que
	guard      sync.Mutex
	cond      *sync.Cond
}

func (self *eventQueue) pushEvent(event interface{}) error {
	self.guard.Lock()
	ele := &queElement{data:event}
	self.evList.push(ele)
	self.guard.Unlock()
	self.cond.Signal()
	return nil
}

func (self *eventQueue) pushCo(co *coroutine) error {
	self.guard.Lock()
	ele := &queElement{data:co}
	self.coList.push(ele)
	self.guard.Unlock()
	self.cond.Signal()
	return nil
}

func (self *eventQueue) pop() (int,interface{}) {
	self.guard.Lock()
	for self.evList.empty() && self.coList.empty() {
		self.cond.Wait()
	}

	var tt int
	var data interface{}

	if !self.coList.empty() {
		tt = type_co
		data = self.coList.pop().data
	}else {
		tt = type_event
		data = self.evList.pop().data
	}

	self.guard.Unlock()
	return tt,data

}



type CoopScheduler struct {
	coPool       coList              //free coroutine	
	queue        eventQueue
	current     *coroutine         //当前正在运行的go程序
	onEvent      func(interface{})
	selfCo       coroutine
	coCount      int
	reserveCount int
	started      bool
	closed       bool
	mtx          sync.Mutex
}

func NewCoopScheduler(onEvent func(interface{})) *CoopScheduler {

	if nil == onEvent {
		return nil
	}

	sche := &CoopScheduler{}
	sche.onEvent      = onEvent
	sche.queue        = eventQueue{}
	sche.queue.cond   = sync.NewCond(&sche.queue.guard)
	sche.reserveCount = 10000
	sche.selfCo       = coroutine{que:make(chan interface{})}
	sche.coPool       = coList{head:nil,tail:nil,size:0} 
	return sche
}

func (this *CoopScheduler) yild () {
	this.selfCo.Yild()
}

func (this *CoopScheduler) resume() {
	this.selfCo.Resume(1)
}

func (this *CoopScheduler) Await(function func()) {
	co := this.current
	/* 唤醒调度go程，让它可以调度其它任务
	*  因此function()现在处于并行执行，可以在里面调用线程安全的阻塞或耗时运算
	*/
	this.resume()
	function()
	//将自己添加到待唤醒通道中，然后Wait等待被唤醒后继续执行
	this.queue.pushCo(co)
	co.Yild()
}

func (this *CoopScheduler) PostEvent(data interface{}) {
	if this.closed {
		return
	}
	this.queue.pushEvent(data)
}

func (this *CoopScheduler) newCo() {
	for i := 0;i < 10;i++{
		co := &coroutine{que:make(chan interface{})}
		this.coPool.PushFront(co)
		go func(){
			for {
				e := co.Yild()
				if nil == e {
					this.coCount--
					this.resume()
					return
				}

				this.onEvent(e)

				if this.coCount > this.reserveCount {
					//co数量超过保留大小，终止
					this.coCount--
					this.resume()
					return					
				}else{
					this.coPool.PushFront(co)
					this.resume()
				}
			}
		}()
	}
	this.coCount += 10
	fmt.Printf("coCount:%d\n",this.coCount)
}

func (this *CoopScheduler) runTask(e interface{}){
	for {
		co := this.coPool.Pop()
		if nil == co {
			this.newCo()
		}else{
			//获取一个空闲的go程，用e将其唤醒，然后将自己投入到等待中
			this.current = co
			co.Resume(e)
			this.yild()
			return
		}
	}
}


func (this *CoopScheduler) Start() {

	this.mtx.Lock()
	if this.closed || this.started {
		this.mtx.Unlock()
		return
	}
	this.started = true
	this.mtx.Unlock()

	for {
		tt,ele := this.queue.pop()
		if tt == type_event {
			if !this.closed {
				this.runTask(ele)
			}
		}else {
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
					co.Resume(nil)//发送nil，通告停止
					this.yild()
				}else {
					break
				}
			}			
		}

		if this.closed && this.coCount == 0 {
			return
		}

	}
}

func (this *CoopScheduler) Close() {
	this.mtx.Lock()
	if !this.closed {
		this.closed = true
	}
	this.mtx.Unlock()
}

