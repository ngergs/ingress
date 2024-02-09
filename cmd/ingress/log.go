package main

import (
	"github.com/go-logr/logr"
	"github.com/rs/zerolog"
	"maps"
)

// set logger for operator sdk
var _ logr.LogSink = &logWrapper{}

type logWrapper struct {
	Logger           zerolog.Logger
	additionalValues map[string]interface{}
}

func (l *logWrapper) Init(_ logr.RuntimeInfo) {
	l.additionalValues = make(map[string]interface{})
}

func (l *logWrapper) Enabled(level int) bool {
	// zerolog has levels -1 (trace) to 5 (panic) while logr has levels >=0 with 'higher meaning "less important"'
	return level <= 5-int(l.Logger.GetLevel())
}

func (l *logWrapper) Info(_ int, msg string, keysAndValues ...interface{}) {
	event := l.Logger.Info()
	l.handleKeyValsMsg(event, msg, keysAndValues)
}

func (l *logWrapper) Error(err error, msg string, keysAndValues ...interface{}) {
	event := l.Logger.Error().Err(err)
	l.handleKeyValsMsg(event, msg, keysAndValues)
}

// WithValues returns a logger with the given values sets. The internal ap of values is shallow copied.
// nolint: ireturn // needed to implement the logr.LogSink interface
func (l *logWrapper) WithValues(keysAndValues ...interface{}) logr.LogSink {
	result := l.shallowCopy()
	if len(keysAndValues)%2 != 0 {
		l.Logger.Warn().Msgf("could not parse additional key/values, array has odd length, dropped: %v", keysAndValues[len(keysAndValues)-1])
		keysAndValues = keysAndValues[:len(keysAndValues)-2]
	}
	for i := 0; i < len(keysAndValues); i += 2 {
		if key, ok := keysAndValues[i].(string); ok {
			result.additionalValues[key] = keysAndValues[i+1]
		} else {
			l.Logger.Warn().Msgf("could not parse additional keys for log message, key is not of type string: %v", keysAndValues[i])
		}
	}
	return result
}

// WithName returns a logger with the given values sets. The internal ap of values is shallow copied.
// nolint: ireturn // needed to implement the logr.LogSink interface
func (l *logWrapper) WithName(name string) logr.LogSink {
	result := l.shallowCopy()
	result.additionalValues["name"] = name
	return result
}

// shallowCopy returns a copy of this logger, the additionalValues as well as the referenced upstream logger are shallow copied.
func (l *logWrapper) shallowCopy() *logWrapper {
	result := &logWrapper{
		Logger:           l.Logger,
		additionalValues: make(map[string]interface{}),
	}
	maps.Copy(result.additionalValues, l.additionalValues)
	return result
}

// handleKeyValsMsg handles the passed msg and the generic list of key value pairs
func (l *logWrapper) handleKeyValsMsg(event *zerolog.Event, msg string, keysAndValues []interface{}) {
	if len(keysAndValues)%2 != 0 {
		l.Logger.Warn().Msgf("could not parse additional key/values for log message, array has odd length, dropped: %v", keysAndValues[len(keysAndValues)-1])
		keysAndValues = keysAndValues[:len(keysAndValues)-2]
	}
	for k, v := range l.additionalValues {
		event = event.Any(k, v)
	}
	for i := 0; i < len(keysAndValues); i += 2 {
		if key, ok := keysAndValues[i].(string); ok {
			event = event.Any(key, keysAndValues[i+1])
		} else {
			l.Logger.Warn().Msgf("could not parse additional keys for log message, key is not of type string: %v", keysAndValues[i])
		}
	}
	event.Msg(msg)
}
