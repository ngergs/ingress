package main

import (
	"flag"
	"fmt"
	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	"net"
	"os"
	"strconv"
	"strings"

	stdlog "log"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

//nolint:gomnd
var (
	version               = "snapshot"
	accessLog             = flag.Bool("access-log", true, "Prints an access log.")
	debugLogging          = flag.Bool("debug", false, "Log debug level")
	help                  = flag.Bool("help", false, "Prints the help.")
	prettyLogging         = flag.Bool("pretty", false, "Activates zerolog pretty logging")
	hostIpString          = flag.String("host-ip", "", "Host IP addresses. Optional, but needs to be set if the ingress status should be updated.")
	hostIp                net.IP
	httpPort              = flag.Int("http-port", 8080, "TCP-Port for the HTTP endpoint")
	httpsPort             = flag.Int("https-port", 8443, "TCP-Port for the HTTPs endpoint")
	http3Enabled          = flag.Bool("http3", false, "Whether http3 is enabled")
	http3Port             = flag.Int("http3-port", 8444, "UDP-Port for the HTTP3 endpoint. Note that Kubernetes merges ContainerPort configs using only the port (not combined with the protocol) as key.")
	http2AltSvcPort       = flag.Int("http2-alt-svc", 443, "h2 TCP-Port for the Alt-Svc HTTP-Header. May differ from https-port e.g. when a container with port mapping or load balancer with port mappings are used.")
	http3AltSvcPort       = flag.Int("http3-alt-svc", 443, "h3 UDP-Port for the Alt-Svc HTTP-Header. May differ from http3-port e.g. when a container with port mapping or load balancer with port mappings are used.")
	hstsEnabled           = flag.Bool("hsts", false, "Set HSTS-Header")
	hstsMaxAge            = flag.Int("hsts-max-age", 63072000, "Max-Age for the HSTS-Header, only relevant if hsts is activated.")
	hstsIncludeSubdomains = flag.Bool("hsts-subdomains", true, "Whether HSTS if activated should add the includeSubdomains directive.")
	hstsPreload           = flag.Bool("hsts-preload", false, "Whether the HSTS preload directive should be active.")
	healthPort            = flag.Int("health-port", 8081, "TCP-Port under which the health check endpoint runs.")
	healthPath            = flag.String("health-path", "/health", "Path under which the health endpoint runs.")
	idleTimeout           = flag.Int("idle-timeout", 30, "Timeout for idle TCP connections with keep-alive in seconds.")
	ingressClassName      = flag.String("ingress-class-name", "ingress", "Corresponds to spec.ingressClassName. Only ingress definitions that match these are evaluated.")
	k8sClientQps          = flag.Int("k8s-client-qps", 20, "Query per second threshold above which client throttling occurs")
	k8sClientBurst        = flag.Int("k8s-client-burst", 40, "Query per second absolute threshold for client throttling")
	metricsNamespace      = flag.String("metrics-namespace", "ingress", "Prometheus namespace for the collected metrics.")
	metricsPort           = flag.Int("metrics-port", 9090, "TCP-Port under which the metrics endpoint runs.")
	readTimeout           = flag.Int("read-timeout", 10, "Timeout to read the entire request in seconds.")
	readinessPath         = flag.String("ready-path", "/ready", "Path under which the ready endpoint runs (health port).")
	shutdownTimeout       = flag.Int("shutdown-timeout", 10, "Timeout to graceful shutdown the reverse proxy in seconds.")
	shutdownDelay         = flag.Int("shutdown-delay", 5, "Delay before shutting down the server in seconds. To make sure that the load balancing of the surrounding infrastructure had time to update.")
	writeTimeout          = flag.Int("write-timeout", 10, "Timeout to write the complete response in seconds.")
	hstsConfig            *HstsConfig
)

// HstsConfig holds the setting for HSTS (HTTP Strict Transport Security)
type HstsConfig struct {
	MaxAge            int
	IncludeSubdomains bool
	Preload           bool
}

// setup parses the config files and returns a logr.Logger to pass to operator sdk
func setup() logr.Logger {
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
	logrLogger := logr.New(&logWrapper{Logger: log.Logger})
	klog.SetLogger(logrLogger)
	log.Info().Msgf("This is ingress version %s", version)
	return logrLogger
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
