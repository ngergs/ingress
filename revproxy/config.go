package revproxy

import (
	"time"
)

// Config is a data structure that holds the config options for the reverse proxy
type Config struct {
	BackendTimeout time.Duration
}

var defaultConfig = Config{
	BackendTimeout: time.Duration(20) * time.Second,
}

// ConfigOption is used to implement the functional parameter pattern for the reverse proxy
type ConfigOption func(*Config)

// BackendTimeout sets the timeout for waiting for the backend response for the reverse proxy
func BackendTimeout(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.BackendTimeout = timeout
	}
}

// applyOptions applied the given variadic options to the config.
func (config *Config) applyOptions(options ...ConfigOption) {
	for _, option := range options {
		option(config)
	}
}

// clone creates a deep copy of the config
func (config *Config) clone() *Config {
	return &Config{
		BackendTimeout: config.BackendTimeout,
	}
}
