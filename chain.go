package xcron

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"xcron/log"
)

// JobWrapper 装饰器，修饰任务
type JobWrapper func(Job) Job

// Chain 一个装饰链，装饰任务
type Chain struct {
	wrappers []JobWrapper
}

func NewChain(c ...JobWrapper) Chain {
	return Chain{c}
}

func (c Chain) Then(j Job) Job {
	for i := range c.wrappers {
		j = c.wrappers[len(c.wrappers)-i-1](j)
	}
	return j
}

// Recover 包装器，日志打印pianc
func Recover(logger log.Logger) JobWrapper {
	return func(j Job) Job {
		return FuncJob(func() {
			defer func() {
				if r := recover(); r != nil {
					const size = 64 << 10
					buf := make([]byte, size)
					buf = buf[:runtime.Stack(buf, false)]
					err, ok := r.(error)
					if !ok {
						err = fmt.Errorf("%v", r)
					}
					logger.Error(err, "panic", "stack", "...\n"+string(buf))
				}
			}()
			j.Run()
		})
	}
}

// DelayIfStillRunning 针对同一任务，序列化作业，延迟后续的运行，直到前一个作业完成。延迟超过一分钟后运行的作业会将延迟记录在Info中
func DelayIfStillRunning(logger log.Logger) JobWrapper {
	return func(j Job) Job {
		var mu sync.Mutex
		return FuncJob(func() {
			start := time.Now()
			mu.Lock()
			defer mu.Unlock()
			if dur := time.Since(start); dur > time.Minute {
				logger.Info("delay", "duration", dur)
			}
			j.Run()
		})
	}
}

// SkipIfStillRunning
func SipIfStillRunning(logger log.Logger) JobWrapper {
	return func(j Job) Job {
		var ch = make(chan struct{}, 1)
		ch <- struct{}{}
		return FuncJob(func() {
			select {
			case v := <-ch:
				j.Run()
				ch <- v
			default:
				logger.Info("skip")
			}
		})
	}
}
