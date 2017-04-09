package benchmark

import (
	"fmt"
	"sync/atomic"
	"time"
	"testing"
	"coop-go"
)


func TestSche(t *testing.T) {


	c1 := int32(0)
	c2 := int32(0)
	count := int32(0)
	
	tickchan := time.Tick(time.Millisecond * time.Duration(1000))
	go func(){
		for{ 
			_ = <- tickchan
			tmp := atomic.LoadInt32(&count)
			atomic.StoreInt32(&count,0)	
			fmt.Printf("count:%d\n",tmp)

		}
	}()


	var p *coop.CoopScheduler


	p = coop.NewCoopScheduler(func (e interface{}){
		atomic.AddInt32(&c1,1)
		atomic.AddInt32(&count,1)
		c2++
		if c1 != c2 {
			fmt.Printf("not equal,%d,%d\n",c1,c2)
		}

		if c2 >= 8000000 {
			p.Close()
			return
		}

		/*p.Await(func () {
			time.Sleep(time.Millisecond * time.Duration(10))
		})*/
		
		p.PostEvent(1)
	})

	for i := 0; i < 10000; i++ {
		p.PostEvent(1)
	}


	p.Start()

	fmt.Printf("scheduler stop,total taskCount:%d\n",c2)


}