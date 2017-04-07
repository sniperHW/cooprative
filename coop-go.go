package coop

import(
	"sync"
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
	type_task   = 1
	type_resume = 2
)

type queElement struct {
	tt   int
	data interface {}
}

type CoopScheduler struct {
	coPool       coList              //free coroutine	
	queue        chan *queElement
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
	sche := &CoopScheduler{}
	sche.onEvent      = onEvent
	sche.queue        = make(chan *queElement,65535)
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

func (this *CoopScheduler) Call(function func()) {
	co := this.current
	/* 唤醒调度go程，让它可以调度其它任务
	*  因此function()现在处于并行执行，可以在里面调用线程安全的阻塞或耗时运算
	*/
	this.resume()
	function()
	//将自己添加到待唤醒通道中，然后Wait等待被唤醒后继续执行
	this.queue <- &queElement{tt:type_resume,data:co}
	co.Yild()
}

func (this *CoopScheduler) PostEvent(data interface{}) {
	if this.closed {
		return
	}

	this.queue <- &queElement{tt:type_task,data:data}
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
	//fmt.Printf("coCount:%d\n",this.coCount)
}

func (this *CoopScheduler) runTask(e interface{}){
	//fmt.Printf("runTask\n")
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
		qele := <- this.queue
		if qele.tt == type_task {
			if !this.closed {
				this.runTask(qele.data)
			}
		}else {
			co := qele.data.(*coroutine)
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
