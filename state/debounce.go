package state

import (
	"context"
	"time"
)

// debouncer is internally used struct for the debounce function to debounce spammed calls to the callback func.
type debouncer struct {
	callback       func()
	bufferDuration time.Duration
	triggerChan    chan time.Time
}

// debounce receives a function callback and wraps it.
// The wrapper debounces spammed calls to it. The callback is called after a number of calls to the wrapper once when for the bufferDuration no additional call has occured.
// This means that if the wrapper is continuously called, the callback will never be called when wrapped with debounce.
// ctx should be cancelled when the debounced function is no longer needed to stop a go coroutine that handles the core debounce logic.
// If not resources will be leaked.
func debounce(ctx context.Context, bufferDuration time.Duration, callback func()) func() {
	debouncer := &debouncer{
		callback:       callback,
		bufferDuration: bufferDuration,
		triggerChan:    make(chan time.Time, 1024),
	}
	go debouncer.listenTrigger(ctx)
	return debouncer.pushNewTrigger
}

// pushNewTrigger is internally used by debounce. It corresponds to the returned wrapper and adds a triiger to the debouncer internal trigger channel.
func (debouncer *debouncer) pushNewTrigger() {
	debouncer.triggerChan <- time.Now()
}

// listenTrigger is internally started by debounce and implements the core debounce logic by listening to the debouncer internal trigger channel.
// Can be stopped  by using a cancelable context. Cancelling is required to free resources (this functon as a go coroutine and the debouncer struct).
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
