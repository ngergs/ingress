package revproxy

import "sync/atomic"

// atomicVal is a wrapper that uses generics to ensure type safety when using atomic.Value
type atomicVal[T any] struct {
	val atomic.Value
}

// Store the value of the wrapped atomic.Value
func (atomicVal *atomicVal[T]) Store(val T) {
	atomicVal.val.Store(val)
}

// Load loads and casts the value of the wrapped atomic.Value.
// ok reflects if a value has been loaded. If ok is false, val will be the zero value of the type T.
func (atomicVal *atomicVal[T]) Load() (val T, ok bool) {
	result := atomicVal.val.Load()
	if result == nil {
		ok = false
		return
	}
	return result.(T), true
}
