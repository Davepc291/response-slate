package recordings

import (
	"errors"
	"time"
)

type Options struct {
	Directory     string
	Timezone      string
	PollInterval  time.Duration
	StableFor     time.Duration
	MaxWait       time.Duration
	RetryInterval time.Duration
	MaxAttempts   int
}

func DefaultOptions() Options {
	return Options{Timezone: "America/New_York", PollInterval: time.Second,
		StableFor: 3 * time.Second, MaxWait: 2 * time.Minute,
		RetryInterval: 5 * time.Second, MaxAttempts: 3}
}

func (o Options) Validate() error {
	if _, err := time.LoadLocation(o.Timezone); err != nil || o.Timezone == "" || o.Timezone == "Local" {
		return errors.New("invalid GFR_RECORDING_TIMEZONE")
	}
	if o.PollInterval < 10*time.Millisecond || o.PollInterval > time.Minute ||
		o.StableFor < o.PollInterval || o.StableFor > 10*time.Minute ||
		o.MaxWait <= o.StableFor || o.MaxWait > time.Hour ||
		o.RetryInterval < o.PollInterval || o.RetryInterval > 10*time.Minute ||
		o.MaxAttempts < 1 || o.MaxAttempts > 20 {
		return errors.New("invalid recording watcher limits")
	}
	return nil
}
