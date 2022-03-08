package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ngergs/ingress/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var accessLog = flag.Bool("access-log", true, "Prints an access log.")
var debugLogging = flag.Bool("debug", false, "Log debug level")
var help = flag.Bool("help", false, "Prints the help.")
var prettyLogging = flag.Bool("pretty", false, "Activates zerolog pretty logging")
var httpPort = flag.Int("http-port", 8080, "Port for the HTTP endpoint")
var httpsPort = flag.Int("https-port", 8443, "Port for the HTTPs endpoint")
var hstsEnabled = flag.Bool("hsts", false, "Set HSTS-Header")
var hstsMaxAge = flag.Int("hsts-max-age", 63072000, "Max-Age for the HSTS-Header, only relevant if hsts is activated.")
var hstsIncludeSubdomains = flag.Bool("hsts-subdomains", true, "Whether HSTS if activated should add the includeSubdomains directive.")
var hstsPreload = flag.Bool("hsts-preload", false, "Whether the HSTS preload directive should be active.")
var health = flag.Bool("health", true, "Whether to start the health check endpoint (/ under a separate port)")
var healthAccessLog = flag.Bool("health-access-log", false, "Prints an access log for the health check endpoint to stdout.")
var healthPort = flag.Int("health-port", 8081, "Different port under which the health check endpoint runs.")
var hstsConfig *server.HSTSconfig

func setup() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s {options} [target-path]\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *help {
		flag.Usage()
		os.Exit(0)
	}
	if *debugLogging {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	if *prettyLogging {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	hstsConfig = &server.HSTSconfig{
		Enabled:           *hstsEnabled,
		MaxAge:            *hstsMaxAge,
		IncludeSubdomains: *hstsIncludeSubdomains,
		Preload:           *hstsPreload,
	}
}
