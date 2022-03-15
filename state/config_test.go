package state

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConfig(t *testing.T) {
	config := &Config{
		DebounceDuration: time.Duration(0),
	}
	timeout := time.Duration(10) * time.Second
	configWithTimeout := config.clone()
	assert.Equal(t, config, configWithTimeout)
	configWithTimeout.applyOptions(DebounceDuration(timeout))
	//make shure that clone worked and the original config has not been changed
	assert.Equal(t, time.Duration(0), config.DebounceDuration)
	assert.Equal(t, timeout, configWithTimeout.DebounceDuration)
}
