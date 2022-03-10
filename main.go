package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ngergs/ingress/revproxy"
	"github.com/ngergs/ingress/state"
	"github.com/rs/zerolog/log"

	websrv "github.com/ngergs/websrv/server"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// main starts the ingress controller
func main() {
	setup()
	var wg sync.WaitGroup
	sigtermCtx := websrv.SigTermCtx(context.Background())

	k8sconfig, err := setupk8s()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to setup Kubernetes client")
	}

	backendTimeout := time.Duration(*readTimeout+*writeTimeout) * time.Second
	ingressStateManager := state.New(sigtermCtx, k8sconfig, *ingressClassName)
	reverseProxy := revproxy.New(ingressStateManager,
		revproxy.HttpPort(*httpPort),
		revproxy.HttpsPort(*httpsPort),
		revproxy.ReadTimeout(time.Duration(*readTimeout)*time.Second),
		revproxy.WriteTimeout(time.Duration(*writeTimeout)*time.Second),
		revproxy.BackendTimeout(backendTimeout),
		revproxy.Optional(revproxy.Hsts(*hstsMaxAge, *hstsIncludeSubdomains, *hstsPreload), *hstsEnabled))
	if err != nil {
		log.Fatal().Err(err).Msg("failed to setup reverse proxy")
	}
	// start listening to state updated and forward them to the reverse proxy
	go forwardUpdates(ingressStateManager, reverseProxy)

	errChan := make(chan error)
	middleware := []websrv.HandlerMiddleware{
		websrv.Optional(websrv.AccessLog(), *accessLog),
		websrv.RequestID(),
	}

	httpServer, err := reverseProxy.GetServerHttp(sigtermCtx, middleware...)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get HTTP server setup")
	}
	websrv.AddGracefulShutdown(sigtermCtx, &wg, httpServer, time.Duration(*shutdownTimeout)*time.Second)
	go func() { errChan <- httpServer.ListenAndServe() }()

	httpsServer, tlsListener, err := reverseProxy.GetServerHttps(sigtermCtx, middleware...)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get HTTPs server setup")
	}
	websrv.AddGracefulShutdown(sigtermCtx, &wg, httpsServer, time.Duration(*shutdownTimeout)*time.Second)
	go func() { errChan <- httpServer.Serve(tlsListener) }()

	if *health {
		healthServer := getHealthServer(func() bool { return ingressStateManager.Ready })
		websrv.AddGracefulShutdown(sigtermCtx, &wg, healthServer, time.Duration(*shutdownTimeout)*time.Second)
		go func() { errChan <- healthServer.ListenAndServe() }()
	}

	go logErrors(errChan)
	wg.Wait()
}

// setupk8s reads the cluster k8s configuration. If none is available the ~/.kube/config file is used as a fallback for local development.
// For providers other than GKE additional imports have to be provided for this fallback to work.
func setupk8s() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", filepath.Join(os.Getenv("HOME"), ".kube", "config"))
		if err != nil {
			return nil, err
		}
	}
	return config, nil
}

// forwardUpdates listens to the update channel from the stateManager and calls the LoadIngressState method of the reverse proxy to forwards the results.
func forwardUpdates(stateManager *state.IngressStateManager, reverseProxy *revproxy.ReverseProxy) {
	for state := range stateManager.GetStateChan() {
		err := reverseProxy.LoadIngressState(state)
		if err != nil {
			log.Error().Err(err).Msg("failed to apply updated state")
		}
	}
}

// startHealthserver initializes the conditional health server.
func getHealthServer(condition func() bool) *http.Server {
	healthServer := websrv.Build(*healthPort,
		websrv.HealthCheckConditionalHandler(condition),
		websrv.Optional(websrv.AccessLog(), *healthAccessLog),
	)
	log.Info().Msgf("Starting healthcheck server on port %d", *healthPort)
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
