package state

import (
	"context"
	"time"
)

type debouncer struct {
	callback       func()
	bufferDuration time.Duration
	triggerChan    chan time.Time
}

func debounce(ctx context.Context, bufferDuration time.Duration, callback func()) func() {
	debouncer := &debouncer{
		callback:       callback,
		bufferDuration: bufferDuration,
		triggerChan:    make(chan time.Time, 1024),
	}
	go debouncer.listenTrigger(ctx)
	return debouncer.pushNewTrigger
}

func (debouncer *debouncer) pushNewTrigger() {
	debouncer.triggerChan <- time.Now()
}

func (debouncer *debouncer) listenTrigger(ctx context.Context) {
	lastTriggerTime := time.Now()
	triggerOpen := false
	ticker := time.NewTicker(debouncer.bufferDuration)
	for {
		select {
		case lastTriggerTime = <-debouncer.triggerChan:
			triggerOpen = true
		case ticktime := <-ticker.C:
			if triggerOpen && ticktime.Sub(lastTriggerTime) > debouncer.bufferDuration {
				triggerOpen = false
				debouncer.callback()
			}
		case <-ctx.Done():
			ticker.Stop()
			return
		}
	}
}
