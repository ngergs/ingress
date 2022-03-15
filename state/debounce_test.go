package state

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDebounce(t *testing.T) {
	var hasBeenCalled uint64 = 0
	timeStep := time.Duration(10) * time.Millisecond
	fn := debounce(context.Background(), timeStep, func() { atomic.AddUint64(&hasBeenCalled, 1) })
	for i := 0; i < 3; i++ {
		fn()
	}
	assert.Equal(t, uint64(0), atomic.LoadUint64(&hasBeenCalled))
	time.Sleep(timeStep)
	assert.Equal(t, uint64(1), atomic.LoadUint64(&hasBeenCalled))
	time.Sleep(2 * timeStep)
	assert.Equal(t, uint64(1), atomic.LoadUint64(&hasBeenCalled))
}

func TestDebounceCancel(t *testing.T) {
	var hasBeenCalled uint64 = 0
	timeStep := time.Duration(10) * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	fn := debounce(ctx, timeStep, func() { atomic.AddUint64(&hasBeenCalled, 1) })
	for i := 0; i < 3; i++ {
		fn()
	}
	cancel()
	assert.Equal(t, uint64(0), atomic.LoadUint64(&hasBeenCalled))
	time.Sleep(2 * timeStep)
	assert.Equal(t, uint64(0), atomic.LoadUint64(&hasBeenCalled))
}
