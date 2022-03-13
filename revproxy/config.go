package revproxy

import (
	"time"
)

// Config is a data structure that holds the config options for the reverse proxy
type Config struct {
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
	BackendTimeout  time.Duration
}

var defaultConfig = Config{
	ReadTimeout:     time.Duration(10) * time.Second,
	WriteTimeout:    time.Duration(10) * time.Second,
	ShutdownTimeout: time.Duration(10) * time.Second,
	BackendTimeout:  time.Duration(20) * time.Second,
}

// ConfigOption is used to implement the functional parameter pattern for the reverse proxy
type ConfigOption func(*Config)

// ReadTimeout sets the read timeout for the server
func ReadTimeout(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.ReadTimeout = timeout
	}
}

// WriteTimeout sets the write timeout for the server
func WriteTimeout(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.WriteTimeout = timeout
	}
}

// ShutdownTimeout sets the timeout for shutting down gracefully
func ShutdownTimeout(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.ShutdownTimeout = timeout
	}
}

// BackendTimeout sets the timeout for waiting for the backend response for the reverse proxy
func BackendTimeout(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.BackendTimeout = timeout
	}
}

// Optional applies the option only if active is true
func Optional(option ConfigOption, active bool) ConfigOption {
	return func(config *Config) {
		if active {
			option(config)
		}
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
		ReadTimeout:     config.ReadTimeout,
		WriteTimeout:    config.WriteTimeout,
		ShutdownTimeout: config.ShutdownTimeout,
		BackendTimeout:  config.BackendTimeout,
	}
}
