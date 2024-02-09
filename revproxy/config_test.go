package revproxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	config := &Config{
		BackendTimeout: time.Duration(0),
	}
	timeout := time.Duration(10) * time.Second
	configWithTimeout := config.clone()
	require.Equal(t, config, configWithTimeout)
	configWithTimeout.applyOptions(BackendTimeout(timeout))
	// make sure that clone worked and the original config has not been changed
	require.Equal(t, time.Duration(0), config.BackendTimeout)
	require.Equal(t, timeout, configWithTimeout.BackendTimeout)
}
