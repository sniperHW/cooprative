package cooprative

//go test -covermode=count -v -coverprofile=coverage.out -run=.
//go tool cover -html=coverage.out
import (
	//"github.com/stretchr/testify/assert"
	"fmt"
	"runtime"
	"testing"
	"time"
)

func TestCoop(t *testing.T) {

	{
		s := NewScheduler()
		s.Run(func(_ *Scheduler) {
			s.Close()
			s.Await(time.Sleep, time.Second)
			fmt.Println("awake")
		})

		s.Start()
	}

	{

		c1 := int32(0)
		c2 := int32(0)
		count := int32(0)
		ok := false

		die := make(chan struct{})

		Run(func(_ *Scheduler) {
			for !ok {
				Await(time.Sleep, time.Second)
				fmt.Printf("count:%d\n", count)
				count = 0
			}
			close(die)
		})

		for i := 0; i < 10000; i++ {
			Run(func() {
				for {
					c1++
					count++
					c2++
					if c1 != c2 {
						fmt.Printf("not equal,%d,%d\n", c1, c2)
					}

					if c2 >= 5000000 {
						ok = true
						return
					}

					Await(runtime.Gosched)
				}
			})
		}

		<-die

	}
}
