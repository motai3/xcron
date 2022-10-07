package xcron_test

import (
	"fmt"
	"github.com/motai3/xcron"
	"github.com/stretchr/testify/assert"
	"testing"
)

func Test_cronExit(t *testing.T) {
	cron := xcron.New()
	data := 0
	data1 := 0
	c := make(chan struct{}, 1)
	var f, f1 func()
	f = func() {
		data++
		fmt.Println("f run")
		if data == 2 {
			xcron.FuncExit()
		}
	}
	f1 = func() {
		data1++
		fmt.Println("f1 run")
		if data1 == 5 {
			c <- struct{}{}
			xcron.FuncExit()

		}
	}
	cron.AddFunc("0/1 * * * * *", f)
	cron.AddFunc("0/1 * * * * *", f1)
	cron.Run()
	select {
	case <-c:
		fmt.Println("end")
	}
	assert.Equal(t, data, 2)
	assert.Equal(t, data1, 5)
}
