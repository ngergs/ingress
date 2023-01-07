package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	stdlog "log"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var version = "snapshot"
var accessLog = flag.Bool("access-log", true, "Prints an access log.")
var debugLogging = flag.Bool("debug", false, "Log debug level")
var help = flag.Bool("help", false, "Prints the help.")
var prettyLogging = flag.Bool("pretty", false, "Activates zerolog pretty logging")
var hostIpString = flag.String("host-ip", "", "Host IP addresses. Optional, but needs to be set if the ingress status should be updated.")
var hostIp net.IP
var httpPort = flag.Int("http-port", 8080, "TCP-Port for the HTTP endpoint")
var httpsPort = flag.Int("https-port", 8443, "TCP-Port for the HTTPs endpoint")
var http3Enabled = flag.Bool("http3", false, "Whether http3 is enabled")
var http3Port = flag.Int("http3-port", 8444, "UDP-Port for the HTTP3 endpoint. Note that Kubernetes merges ContainerPort configs using only the port (not combined with the protocol) as key.")
var http2AltSvcPort = flag.Int("http2-alt-svc", 443, "h2 TCP-Port for the Alt-Svc HTTP-Header. May differ from https-port e.g. when a container with port mapping or load balancer with port mappings are used.")
var http3AltSvcPort = flag.Int("http3-alt-svc", 443, "h3 UDP-Port for the Alt-Svc HTTP-Header. May differ from http3-port e.g. when a container with port mapping or load balancer with port mappings are used.")
var hstsEnabled = flag.Bool("hsts", false, "Set HSTS-Header")
var hstsMaxAge = flag.Int("hsts-max-age", 63072000, "Max-Age for the HSTS-Header, only relevant if hsts is activated.")
var hstsIncludeSubdomains = flag.Bool("hsts-subdomains", true, "Whether HSTS if activated should add the includeSubdomains directive.")
var hstsPreload = flag.Bool("hsts-preload", false, "Whether the HSTS preload directive should be active.")
var health = flag.Bool("health", true, "Whether to start the health check endpoint (/ under a separate port)")
var healthAccessLog = flag.Bool("health-access-log", false, "Prints an access log for the health check endpoint to stdout.")
var healthPort = flag.Int("health-port", 8081, "TCP-Port under which the health check endpoint runs.")
var idleTimeout = flag.Int("idle-timeout", 30, "Timeout for idle TCP connections with keep-alive in seconds.")
var ingressClassName = flag.String("ingress-class-name", "ingress", "Corresponds to spec.ingressClassName. Only ingress definitions that match these are evaluated.")
var k8sClientQps = flag.Int("k8s-client-qps", 20, "Query per second threshold above which client throttling occurs")
var k8sClientBurst = flag.Int("k8s-client-burst", 40, "Query per second absolute threshold for client throttling")
var metrics = flag.Bool("metrics", false, "Whether to start the metrics endpoint (/ under a separate port)")
var metricsAccessLog = flag.Bool("metrics-access-log", false, "Prints an access log for the metrics endpoint to stdout.")
var metricsNamespace = flag.String("metrics-namespace", "ingress", "Prometheus namespace for the collected metrics.")
var metricsPort = flag.Int("metrics-port", 9090, "TCP-Port under which the metrics endpoint runs.")
var readTimeout = flag.Int("read-timeout", 10, "Timeout to read the entire request in seconds.")
var shutdownTimeout = flag.Int("shutdown-timeout", 10, "Timeout to graceful shutdown the reverse proxy in seconds.")
var shutdownDelay = flag.Int("shutdown-delay", 5, "Delay before shutting down the server in seconds. To make sure that the load balancing of the surrounding infrastructure had time to update.")
var writeTimeout = flag.Int("write-timeout", 10, "Timeout to write the complete response in seconds.")
var hstsConfig *HstsConfig

// HstsConfig holds the setting for HSTS (HTTP Strict Transport Security)
type HstsConfig struct {
	MaxAge            int
	IncludeSubdomains bool
	Preload           bool
}

func setup() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s {options}\nOptions:\n", os.Args[0])
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
	if *hstsEnabled {
		hstsConfig = &HstsConfig{
			MaxAge:            *hstsMaxAge,
			IncludeSubdomains: *hstsIncludeSubdomains,
			Preload:           *hstsPreload,
		}
	}
	if *hostIpString != "" {
		hostIp = net.ParseIP(*hostIpString)
		if hostIp == nil {
			log.Warn().Msgf("Host IP is set, but not valid, will be ignored: %s", *hostIpString)
		}
	}

	stdlog.SetFlags(0)
	stdlog.SetOutput(log.Logger)
	log.Info().Msgf("This is ingress version %s", version)
}

// hstsHeader returns the HSTS HTTP-Header value
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
