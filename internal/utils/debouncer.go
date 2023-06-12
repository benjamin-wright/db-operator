package utils

import "time"

type Debouncer struct {
	triggered bool
	debounce  time.Duration
}

func NewDebouncer(debounce time.Duration) Debouncer {
	return Debouncer{
		triggered: false,
		debounce:  debounce,
	}
}

func (d *Debouncer) Trigger() {
	d.triggered = true
}

func (d *Debouncer) Wait() <-chan time.Time {
	if d.triggered {
		d.triggered = false
		return time.After(d.debounce)
	} else {
		return make(<-chan time.Time)
	}
}
