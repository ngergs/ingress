package main

import (
	"context"
	"fmt"
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
	ctx := websrv.SigTermCtx(context.Background())

	reverseProxy, isRdy, err := setupReverseProxy(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not setup reverse proxy")
	}

	middleware, middlewareTLS := setupMiddleware()
	httpServer := getServer(httpPort, reverseProxy.GetHttpsRedirectHandler(), middleware...)
	// port is defined below via listenAndServeTls. Therefore do not set it here to avoid the illusion of it being of relevance here.
	tlsServer := getServer(nil, reverseProxy.GetHandlerProxying(), middlewareTLS...)
	websrv.AddGracefulShutdown(ctx, &wg, httpServer, time.Duration(*shutdownTimeout)*time.Second)
	websrv.AddGracefulShutdown(ctx, &wg, tlsServer, time.Duration(*shutdownTimeout)*time.Second)

	errChan := make(chan error)
	go func() {
		log.Info().Msgf("Listening for HTTP under container port %s", httpServer.Addr[1:])
		errChan <- httpServer.ListenAndServe()
	}()
	go func() { errChan <- listenAndServeTls(ctx, *httpsPort, tlsServer, reverseProxy.TlsConfig()) }()
	if *health {
		healthServer := getHealthServer(isRdy)
		websrv.AddGracefulShutdown(ctx, &wg, healthServer, time.Duration(*shutdownTimeout)*time.Second)
		go func() { errChan <- healthServer.ListenAndServe() }()
	}

	go logErrors(errChan)
	wg.Wait()
}

// setupReverseProxy sets up the Kubernetes Api Client and subsequently sets up everyhing for the reverse proxy.
// This includes automatic updates when the Kubernetes resource status (ingress, service, secrets) changes.
func setupReverseProxy(ctx context.Context) (reverseProxy *revproxy.ReverseProxy, isRdy func() bool, err error) {
	k8sconfig, err := setupk8s()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to setup Kubernetes client: %w", err)
	}

	backendTimeout := time.Duration(*readTimeout+*writeTimeout) * time.Second
	ingressStateManager := state.New(ctx, k8sconfig, *ingressClassName)
	reverseProxy = revproxy.New(
		revproxy.BackendTimeout(backendTimeout))
	if err != nil {
		log.Fatal().Err(err).Msg("failed to setup reverse proxy")
	}
	isRdy = func() bool { return ingressStateManager.Ready }
	// start listening to state updated and forward them to the reverse proxy
	go forwardUpdates(ingressStateManager, reverseProxy)
	return
}

// setupMiddleware constructs the relevant websrv.HandlerMiddleware for the given config
func setupMiddleware() (middleware []websrv.HandlerMiddleware, middlewareTLS []websrv.HandlerMiddleware) {
	middleware = []websrv.HandlerMiddleware{
		websrv.Optional(websrv.AccessLog(), *accessLog),
		websrv.RequestID(),
	}
	middlewareTLS = middleware
	if *hstsEnabled {
		middlewareTLS = append([]websrv.HandlerMiddleware{
			websrv.Header(&websrv.Config{Headers: map[string]string{"Strict-Transport-Security": hstsConfig.hstsHeader()}}),
		}, middlewareTLS...)
	}
	return
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
