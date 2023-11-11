# 协作式go程

### 为什么要协作式go程

考虑如下开发框架，一组网络接收goroutine接收网络包，解包，然后将逻辑包推送到消息队列，由一个单一的逻辑处理goroutine负责从队列中提取逻辑包并处理(这样主处理逻辑中基本上不用考虑多线程竞争的锁问题了)。

如果逻辑包的处理涉及到调用可能会阻塞的函数调用怎么办，如果在处理函数中直接调用这样的函数将导致逻辑处理goroutine被阻塞，无法继续处理队列中被排队的数据包，这将严重降低服务的处理能力。

一种方式是启动一个新的go程去执行阻塞调用，并注册回调函数，当阻塞调用返回后将回调闭包重新push到消息对列中，由逻辑处理goroutine继续处理后续逻辑。但我本人不大喜欢在逻辑处理上使用回调的方式(node的callback hell)。我希望可以线性的编写逻辑代码。

为了实现这个目的，我需要一个类似lua的单线程协作式coroutine调度机制，单线程让使用者不用担心数据竞争,协作式可以让coroutine在执行异步调用前将执行权交出去，等异步结果返回后再将执行权切换回来，线性的执行后续代码。

但是，goroutine天生就是多线程调度执行的，有办法实现这个目标吗？答案是肯定的。

我们可以实现一个逻辑上的单线程，从全局上看，只有唯一一个goroutine可以执行逻辑处理代码。核心思想就是由调度器从任务队列中提取任务，挑选一个空闲的goroutine,将其唤醒并让自己阻塞，当goroutine需要阻塞时就唤醒调度器并将自己阻塞。这样全局上就只有唯一的goroutine在执行逻辑代码。



下面是一个使用示例：

~~~go

func main() {

	c1 := int32(0)
	c2 := int32(0)
	count := int32(0)

	tickchan := time.Tick(time.Millisecond * time.Duration(1000))
	go func() {
		for {
			_ = <-tickchan
			tmp := atomic.LoadInt32(&count)
			atomic.StoreInt32(&count, 0)
			fmt.Printf("count:%d\n", tmp)

		}
	}()

	s := cooprative.NewScheduler()

	var fn func(int)

	fn = func(a int) {

		if a != 0 {
			panic("a != 0")
		}

		atomic.AddInt32(&c1, 1)
		atomic.AddInt32(&count, 1)
		c2++
		if c1 != c2 {
			fmt.Printf("not equal,%d,%d\n", c1, c2)
		}

		if c2 >= 5000000 {
			s.Close()
			return
		}

		s.Await(time.Sleep, time.Millisecond*time.Duration(100))

		s.PostFn(fn, 0)
	}

	for i := 0; i < 10000; i++ {
		s.PostFn(fn, 0)
	}

	s.Start()

	fmt.Printf("scheduler stop,total taskCount:%d\n", c2)

}
~~~



首先用一个任务处理函数作为参数创建调度器。然后向调度器投递任务触发处理循环，最后启动处理。

这里唯一需要关注的是Await,它的参数是一个函数闭包，Await将会在并行的环境下执行传给它的闭包(不释放自己执行权的同时唤醒调度器去调度其它任务),因为这个闭包是并行执行的，所以闭包内不能含有任何非
线程安全的代码，可以将同步的阻塞调用放到闭包中，不用担心阻塞主处理逻辑。Await内部在闭包调用返回之后会将自己阻塞并添加到唤醒队列中等待调度器调度运行。获得运行权之后才从Await调用返回，从Await返回
之后，又回到线程安全的运行环境下。

下面是一个同步获取redis数据的调用:

~~~go

ret := Await(redis.get)

if ret {
  //根据返回值执行处理逻辑
}
~~~



### 可重入actor

actor模型保证单线程语义，简化了开发。考虑如下用例，A，B两个actor，同时向对方请求添加好友。对于不可重入actor,A和B将同时阻塞在等待对方的响应上，导致制死锁。为了避免这种情况需要实现可重入actor。协作式go程正合适用于实现这样的机制,它保证了单线程语义同时提供Await方法，可用于实现不阻塞actor的调用:

~~~go
type actor struct {
	mailBox *cooprative.Scheduler
}

func (a *actor) ReentrantCall(other *actor, fn func(chan struct{})) {
	retChan := make(chan struct{})
	other.OnEvent(func() { fn(retChan) })
	a.mailBox.Await(func() { <-retChan })
}

func (a *actor) Call(other *actor, fn func(chan struct{})) {
	retChan := make(chan struct{})
	other.OnEvent(func() { fn(retChan) })
	<-retChan
}

func (a *actor) Start() {
	a.mailBox.Start()
}

func (a *actor) OnEvent(fn func()) {
	a.mailBox.RunTask(context.Background(), fn)
}

func main() {
	a := &actor{mailBox: cooprative.NewScheduler(cooprative.SchedulerOption{TaskQueueCap: 64})}

	a.Start()

	b := &actor{mailBox: cooprative.NewScheduler(cooprative.SchedulerOption{TaskQueueCap: 64})}

	b.Start()

	a.OnEvent(func() {
		fmt.Println("in A,before a.ReentrantCall")
		a.ReentrantCall(b, func(retchA chan struct{}) {
			fmt.Println("in B,before b.Call")
			//A调用B，B在处理函数中再次调用A
			b.Call(a, func(retchB chan struct{}) {
				fmt.Println("in A")
				retchB <- struct{}{}
			})
			//ReentrantCall可以成功执行
			fmt.Println("b.Call OK")
			retchA <- struct{}{}
		})
		fmt.Println("a.ReentrantCall OK")
	})

	time.Sleep(time.Second)

	a.OnEvent(func() {
		fmt.Println("in A,before a.Call")
		a.Call(b, func(retchA chan struct{}) {
			fmt.Println("in B,before b.Call")
			//A调用B，B在处理函数中再次调用A，此时A阻塞在a.Call上,b.Call投递到a.Mailbox中的func无法被执行
			b.Call(a, func(retchB chan struct{}) {
				fmt.Println("in A")
				retchB <- struct{}{}
			})
			//普通Call产生死锁
			fmt.Println("b.Call OK")
			retchA <- struct{}{}
		})
		fmt.Println("a.Call OK")
	})

	time.Sleep(time.Second)
}

~~~









