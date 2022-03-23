package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strconv"

	"github.com/lucas-clemente/quic-go"
	"github.com/lucas-clemente/quic-go/http3"
	websrv "github.com/ngergs/websrv/server"
	"github.com/rs/zerolog/log"
)

// listenAndServeTls is a wrapper that starts a net.Listener under the given tcp port
// and subsequently listens with the provided http.Server to that listener.
// Blocks until finished just like http.server.ListenAndServe
func listenAndServeTls(ctx context.Context, port int, server *http.Server, tlsConfig *tls.Config) error {
	log.Info().Msgf("Listening for HTTPS under container port tcp/%d", port)
	tlsListener, err := tls.Listen("tcp", ":"+strconv.Itoa(port), tlsConfig)
	if err != nil {
		return err
	}
	return server.Serve(tlsListener)
}

// listenAndServeQuic is a wrapper that starts a quic.EarlyListener under the given udp port
// and subsequently listens with the provided http.Server autowrapped into a http3.Server to that listener.
// Blocks until finished just like http.server.ListenAndServe
func listenAndServeQuic(ctx context.Context, port int, server *http.Server, tlsConfig *tls.Config) error {
	log.Info().Msgf("Listening for HTTP3 under container port udp/%d", port)
	tlsConfig = http3.ConfigureTLSConfig(tlsConfig)
	listener, err := quic.ListenAddrEarly(":"+strconv.Itoa(port), tlsConfig, nil)
	if err != nil {
		return err
	}
	quicServer := http3.Server{Server: server}
	return quicServer.ServeListener(listener)
}

// getServer returns the http.Server to start the http endpoint.
// Middleware is applied in order of occurence, i.e. the first provided middleare sees the request first.
func getServer(port *int, handler http.Handler, handlerSetups ...websrv.HandlerMiddleware) *http.Server {
	server := &http.Server{
		Handler: addMiddleware(handler, handlerSetups...),
	}
	if port != nil {
		server.Addr = ":" + strconv.Itoa(*port)
	}
	return server
}

// addMiddleware is an internal function to apply functional middleware wrapper to a root http.Handler.
func addMiddleware(root http.Handler, handlerSetups ...websrv.HandlerMiddleware) http.Handler {
	for _, handlerSetup := range handlerSetups {
		root = handlerSetup(root)
	}
	return root
}

// startHealthserver initializes the health server.
func getHealthServer() *http.Server {
	healthServer := websrv.Build(*healthPort,
		websrv.HealthCheckHandler(),
		websrv.Optional(websrv.AccessLog(), *healthAccessLog),
	)
	log.Info().Msgf("Starting healthcheck server on port tcp/%d", *healthPort)
	return healthServer
}

// logErrors listens to the provided errChan and logs the received errors
func logErrors(errChan <-chan error) {
	for err := range errChan {
		if errors.Is(err, http.ErrServerClosed) {
			// thrown from listen, serve and listenAndServe during graceful shutdown
			log.Debug().Err(err).Msg("Expected graceful shutdown error")
		} else {
			log.Fatal().Err(err).Msg("Error from server: %v")
		}
	}
}
