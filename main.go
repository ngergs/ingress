package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	k8sconfig, err := setupk8s()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to setup Kubernetes client")
	}

	backendTimeout := time.Duration(*readTimeout+*writeTimeout) * time.Second
	ingressStateManager := state.New(ctx, k8sconfig, *ingressClassName)
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
	go func() { errChan <- reverseProxy.StartHttp(ctx, middleware...) }()
	go func() { errChan <- reverseProxy.StartHttps(ctx, middleware...) }()
	if *health {
		go func() { errChan <- startHealthServer() }()
	}

	for err := range errChan {
		if errors.Is(err, http.ErrServerClosed) {
			// thrown from listen, serve and listenAndServe during graceful shutdown
			log.Debug().Err(err).Msg("expected graceful shutdown error")
		} else {
			log.Fatal().Err(err).Msg("Error starting server: %v")
		}
	}

}

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

func forwardUpdates(stateManager *state.IngressStateManager, reverseProxy *revproxy.ReverseProxy) {
	for state := range stateManager.GetStateChan() {
		err := reverseProxy.LoadIngressState(state)
		if err != nil {
			log.Error().Err(err).Msg("failed to apply updated state")
		}
	}
}

func startHealthServer() error {
	healthServer := websrv.Build(*healthPort,
		websrv.HealthCheckHandler(),
		websrv.Optional(websrv.AccessLog(), *healthAccessLog),
	)
	log.Info().Msgf("Starting healthcheck server on port %d", *healthPort)
	return healthServer.ListenAndServe()
}
