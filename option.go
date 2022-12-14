package xcron

import (
	"github.com/motai3/xcron/log"
	xutil "github.com/motai3/xcron/util"
	"time"
)

type Option func(*Cron)

func WithLocation(loc *time.Location) Option {
	return func(c *Cron) {
		c.location = loc
	}
}

func WithSeconds() Option {
	return WithParser(xutil.NewParser(
		xutil.Second | xutil.Minute | xutil.Hour | xutil.Dom | xutil.Month | xutil.Dow | xutil.Descriptor,
	))
}

func WithParser(p ScheduleParser) Option {
	return func(c *Cron) {
		c.parser = p
	}
}

func WithLogger(logger log.Logger) Option {
	return func(c *Cron) {
		c.logger = logger
	}
}
