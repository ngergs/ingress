package revproxy

import (
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HttpPort        int
	HttpsPort       int
	Hsts            *HstsConfig
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

type HstsConfig struct {
	MaxAge            int
	IncludeSubdomains bool
	Preload           bool
}

var defaultConfig = Config{
	HttpPort:        8080,
	HttpsPort:       8443,
	Hsts:            nil,
	ReadTimeout:     time.Duration(10) * time.Second,
	WriteTimeout:    time.Duration(10) * time.Second,
	ShutdownTimeout: time.Duration(10) * time.Second,
}

func (hsts *HstsConfig) hstsHeader() string {
	if hsts == nil {
		return "max-age=0"
	}
	var result strings.Builder
	result.WriteString("max-age=")
	result.WriteString(strconv.Itoa(hsts.MaxAge))
	if hsts.IncludeSubdomains {
		result.WriteString("; includeSubDomains")
	}
	if hsts.Preload {
		result.WriteString("; preload")
	}
	return result.String()
}

type ConfigOption func(*Config)

func HttpPort(port int) ConfigOption {
	return func(config *Config) {
		config.HttpPort = port
	}
}

func HttpsPort(port int) ConfigOption {
	return func(config *Config) {
		config.HttpsPort = port
	}
}

func Hsts(maxAge int, includeSubdomains bool, preload bool) ConfigOption {
	return func(config *Config) {
		config.Hsts = &HstsConfig{
			MaxAge:            maxAge,
			IncludeSubdomains: includeSubdomains,
			Preload:           preload,
		}
	}
}

func ReadTimeout(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.ReadTimeout = timeout
	}
}

func WriteTimeout(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.WriteTimeout = timeout
	}
}

func ShutdownTimeout(timeout time.Duration) ConfigOption {
	return func(config *Config) {
		config.ShutdownTimeout = timeout
	}
}

func Optional(option ConfigOption, active bool) ConfigOption {
	return func(config *Config) {
		if active {
			option(config)
		}
	}
}

func applyOptions(config *Config, options ...ConfigOption) {
	for _, option := range options {
		option(config)
	}
}
