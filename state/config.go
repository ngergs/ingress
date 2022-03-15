package state

import (
	"time"
)

// Config is a data structure that holds the config options for the kubernetes state
type Config struct {
	//DebounceDuration is the duration for Kubernetes status update debouncing.
	// Only when for this duration no update has been received the status is updated.
	// Defaults to 1 second.
	DebounceDuration time.Duration
}

var defaultConfig = Config{
	DebounceDuration: time.Duration(1) * time.Second,
}

// ConfigOption is used to implement the functional parameter pattern for the kubernetes state
type ConfigOption func(*Config)

// DebounceDuration sets the timeout for waiting for the backend response for the kubernetes state
func DebounceDuration(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.DebounceDuration = timeout
	}
}

// applyOptions applied the given variadic options to the config.
// the argument config option is modified, the returned value is only for ease of use.
func (config *Config) applyOptions(options ...ConfigOption) *Config {
	for _, option := range options {
		option(config)
	}
	return config
}

// clone creates a deep copy of the config
func (config *Config) clone() *Config {
	return &Config{
		DebounceDuration: config.DebounceDuration,
	}
}
