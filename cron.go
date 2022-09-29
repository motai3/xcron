package xcron

import (
	"context"
	"sort"
	"sync"
	"time"
	"xcron/log"
	"xcron/util"
)

// Cron 作业运行中心
type Cron struct {
	entries   []*Entry
	chain     Chain //装饰链
	stop      chan struct{}
	add       chan *Entry
	remove    chan EntryID
	snapshot  chan chan []Entry //cron运行时，用于传输快照的通道
	running   bool
	logger    log.Logger
	runningMu sync.Mutex
	location  *time.Location
	parser    ScheduleParser
	nextID    EntryID //单纯记录当前最大任务编号，每次加1
	jobWaiter sync.WaitGroup
}

type EntryID int

// Entry 任务实体封装
type Entry struct {
	ID         EntryID
	Schedule   Schedule
	Next       time.Time //作业将运行的时间
	Prev       time.Time //该作业最后一次运行的时间
	WrappedJob Job
	Job        Job
}

// Schedule 调度器，调度任务何时运行
type Schedule interface {
	// Next returns the next activation time, later than the given time.
	// Next is invoked initially, and then each time the job is run.
	Next(time.Time) time.Time
}

// ScheduleParser 是一个时间解析器，返回一个Schedule
type ScheduleParser interface {
	Parse(spec string) (Schedule, error)
}

// Job 任务接口
type Job interface {
	Run()
}

type FuncJob func()

func (f FuncJob) Run() { f() }

func (e Entry) Valid() bool {
	return e.ID != 0
}

// byTime 按时间排序的Entry数组
type byTime []*Entry

func (s byTime) Len() int { return len(s) }

func (s byTime) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s byTime) Less(i, j int) bool {
	// 零时的话排在最后，其他情况按时间从小到大排序
	if s[i].Next.IsZero() {
		return false
	}
	if s[j].Next.IsZero() {
		return true
	}
	return s[i].Next.Before(s[j].Next)
}

// New 新建Cron方法，可通过 Option 对一些参数进行设置
func New(opts ...Option) *Cron {
	c := &Cron{
		entries:   nil,
		chain:     NewChain(),
		add:       make(chan *Entry),
		stop:      make(chan struct{}),
		snapshot:  make(chan chan []Entry),
		remove:    make(chan EntryID),
		running:   false,
		runningMu: sync.Mutex{},
		logger:    log.DefaultLogger,
		location:  time.Local,
		parser:    xutil.StandardParser,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Cron) AddFunc(spec string, cmd func()) (EntryID, error) {
	return c.AddJob(spec, FuncJob(cmd))
}

func (c *Cron) AddJob(spec string, cmd Job) (EntryID, error) {
	schedule, err := c.parser.Parse(spec)
	if err != err {
		return 0, err
	}
	return c.Schedule(schedule, cmd), nil
}

func (c *Cron) Schedule(schedule Schedule, cmd Job) EntryID {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	c.nextID++
	entry := &Entry{
		ID:         c.nextID,
		Schedule:   schedule,
		WrappedJob: c.chain.Then(cmd),
		Job:        cmd,
	}
	if !c.running {
		c.entries = append(c.entries, entry)
	} else {
		c.add <- entry
	}
	return entry.ID
}

// SnapshotEntries 返回一个Cron entries 的快照
func (c *Cron) SnapshotEntries() []Entry {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	if c.running {
		replyChan := make(chan []Entry, 1)
		c.snapshot <- replyChan
		return <-replyChan
	}
	return c.entrySnapshot()
}

func (c *Cron) entrySnapshot() []Entry {
	var entries = make([]Entry, len(c.entries))
	for i, e := range c.entries {
		entries[i] = *e
	}
	return entries
}

func (c *Cron) Location() *time.Location {
	return c.location
}

func (c *Cron) Entry(id EntryID) Entry {
	for _, entry := range c.SnapshotEntries() {
		if id == entry.ID {
			return entry
		}
	}
	return Entry{}
}

func (c *Cron) Remove(id EntryID) {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	if c.running {
		c.remove <- id
	} else {
		c.removeEntry(id)
	}
}

func (c *Cron) removeEntry(id EntryID) {
	var entries []*Entry
	for _, e := range c.entries {
		if e.ID != id {
			entries = append(entries, e)
		}
	}
	c.entries = entries
}

func (c *Cron) Start() {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	if c.running {
		return
	}
	c.running = true
	go c.run()
}

func (c *Cron) Run() {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	if c.running {
		return
	}
	c.running = true
	c.run()
}

func (c *Cron) run() {
	c.logger.Info("start")
	now := c.now()
	for _, entry := range c.entries {
		entry.Next = entry.Schedule.Next(now)
		c.logger.Info("schedule", "now", now, "entry", entry.ID, "next", entry.Next)
	}

	for {
		sort.Sort(byTime(c.entries))

		var timer *time.Timer
		if len(c.entries) == 0 || c.entries[0].Next.IsZero() {
			timer = time.NewTimer(100000 * time.Hour)
		} else {
			timer = time.NewTimer(c.entries[0].Next.Sub(now))
		}

		for {
			select {
			case now = <-timer.C:
				//时间副本
				now = now.In(c.location)
				c.logger.Info("wake", "now", now)
				for _, e := range c.entries {
					if e.Next.After(now) || e.Next.IsZero() {
						break
					}
					c.startJob(e.WrappedJob)
					e.Prev = e.Next
					e.Next = e.Schedule.Next(now)
					c.logger.Info("run", "now", now, "entry", e.ID, "next", e.Next)

				}
			case newEntry := <-c.add:
				timer.Stop()
				now = c.now()
				newEntry.Next = newEntry.Schedule.Next(now)
				c.entries = append(c.entries, newEntry)
				c.logger.Info("added", "now", now, "entry", newEntry.ID, "next", newEntry.Next)

			case replyChan := <-c.snapshot:
				replyChan <- c.entrySnapshot()
				continue

			case <-c.stop:
				timer.Stop()
				c.logger.Info("stop")
				return

			case id := <-c.remove:
				timer.Stop()
				now = c.now()
				c.removeEntry(id)
				c.logger.Info("removed", "entry", id)
			}

			break
		}
	}
}

func (c *Cron) Stop() context.Context {
	c.runningMu.Lock()
	defer c.runningMu.Unlock()
	if c.running {
		c.stop <- struct{}{}
		c.running = false
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c.jobWaiter.Wait()
		cancel()
	}()
	return ctx
}

func (c *Cron) startJob(j Job) {
	c.jobWaiter.Add(1)
	go func() {
		defer c.jobWaiter.Done()
		j.Run()
	}()
}

func (c *Cron) now() time.Time {
	return time.Now().In(c.location)
}
