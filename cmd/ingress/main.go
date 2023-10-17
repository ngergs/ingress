package main

import (
	"context"
	"fmt"
	"github.com/ngergs/ingress/state"
	"os"
	"path/filepath"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ngergs/ingress/revproxy"
	"github.com/rs/zerolog/log"

	chi "github.com/go-chi/chi/v5/middleware"
	websrv "github.com/ngergs/websrv/v3/server"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	_ "go.uber.org/automaxprocs"
)

// main starts the ingress controller
func main() {
	setup()
	var wg sync.WaitGroup
	sigtermCtx := websrv.SigTermCtx(context.Background(), time.Duration(*shutdownDelay)*time.Second)

	k8sConfig, err := setupk8s()
	if err != nil {
		log.Fatal().Err(err).Msg("error setting up k8s client")
	}
	mgr, err := setupControllerManager(k8sConfig)
	if err != nil {
		log.Fatal().Err(err).Msg("could not setup controller manager")
	}
	reverseProxy, ingressStateReconciler, err := setupReverseProxy(sigtermCtx, mgr)
	if err != nil {
		log.Fatal().Err(err).Msg("Could not setup reverse proxy")
	}

	middleware, middlewareTLS := setupMiddleware()
	httpServer := getServer(httpPort, reverseProxy.GetHttpsRedirectHandler(), middleware...)
	// port is defined below via listenAndServeTls. Therefore, do not set it here to avoid the illusion of it being of relevance here.
	tlsServer := getServer(nil, reverseProxy.GetHandlerProxying(), middlewareTLS...)
	httpCtx := context.WithValue(sigtermCtx, websrv.ServerName, "http server")
	websrv.AddGracefulShutdown(httpCtx, &wg, httpServer, time.Duration(*shutdownTimeout)*time.Second)
	tlsCtx := context.WithValue(sigtermCtx, websrv.ServerName, "https server")
	websrv.AddGracefulShutdown(tlsCtx, &wg, tlsServer, time.Duration(*shutdownTimeout)*time.Second)
	tlsConfig := getTlsConfig(reverseProxy.GetCertificateFunc())

	errChan := make(chan error)
	go func() {
		log.Info().Msgf("Listening for HTTP under container port tcp/%s", httpServer.Addr[1:])
		errChan <- httpServer.ListenAndServe()
	}()
	go func() { errChan <- listenAndServeTls(*httpsPort, tlsServer, tlsConfig) }()
	if *http3Enabled {
		quicServer := getServer(nil, reverseProxy.GetHandlerProxying(), middlewareTLS...)
		quicCtx := context.WithValue(sigtermCtx, websrv.ServerName, "http3 server")
		websrv.AddGracefulShutdown(quicCtx, &wg, quicServer, time.Duration(*shutdownTimeout)*time.Second)
		go func() { errChan <- listenAndServeQuic(*http3Port, quicServer, tlsConfig) }()
	}

	wg.Add(1)
	go func() {
		log.Info().Msg("starting control manager")
		errChan <- ingressStateReconciler.Start(sigtermCtx)
		log.Debug().Msg("stopped control manager")
		wg.Done()
	}()
	go logErrors(errChan)
	wg.Wait()
	// cleanup
	errors := ingressStateReconciler.CleanIngressStatus(context.Background())
	for _, err := range errors {
		log.Error().Err(err).Msg("could not cleanup ingress state")
	}
}

// setupControllerManager returns a configured controller manager from kubebuilder
func setupControllerManager(k8sConfig *rest.Config) (ctrl.Manager, error) {
	k8sConfig.QPS = float32(*k8sClientQps)
	k8sConfig.Burst = *k8sClientBurst
	log.Info().Msgf("Health check is tcp/%d/%s", *healthPort, *healthPath)
	log.Info().Msgf("Readiness check is  tcp/%d/%s", *healthPort, *readinessPath)
	log.Info().Msgf("Metrics address is tcp/%d//metrics", *metricsPort)
	mgr, err := ctrl.NewManager(k8sConfig, ctrl.Options{
		WebhookServer:          webhook.NewServer(webhook.Options{Port: -1}), //disable webhook server
		HealthProbeBindAddress: fmt.Sprintf(":%d", *healthPort),
		LivenessEndpointName:   *healthPath,
		ReadinessEndpointName:  *readinessPath,
		Metrics:                server.Options{BindAddress: fmt.Sprintf(":%d", *metricsPort)},
	})
	if err != nil {
		return nil, fmt.Errorf("error setting up kubebuilder manager: %v", err)
	}
	if err = mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		return nil, fmt.Errorf("error registering health check for controller manager %v", err)
	}
	if err = mgr.AddReadyzCheck("ready", healthz.Ping); err != nil {
		return nil, fmt.Errorf("error registering ready check for controller manager %v", err)
	}
	return mgr, nil
}

// setupReverseProxy sets up the Kubernetes Api Client and subsequently sets up everything for the reverse proxy.
// This includes automatic updates when the Kubernetes resource status (ingress, service, secrets) changes.
func setupReverseProxy(ctx context.Context, mgr ctrl.Manager) (reverseProxy *revproxy.ReverseProxy, ingressStateReconciler *state.IngressReconciler, err error) {
	backendTimeout := time.Duration(*readTimeout+*writeTimeout) * time.Second
	ingressStateReconciler, err = state.New(mgr, *ingressClassName, hostIp)
	if err != nil {
		return nil, nil, fmt.Errorf("error setting up ingress reconciler: %v", err)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("failed to setup kubebuilder manager:%v", err)
	}
	reverseProxy = revproxy.New(revproxy.BackendTimeout(backendTimeout))

	go forwardUpdates(ctx, ingressStateReconciler, reverseProxy)
	return reverseProxy, ingressStateReconciler, nil
}

// setupMiddleware constructs the relevant websrv.HandlerMiddleware for the given config
func setupMiddleware() (middleware []websrv.HandlerMiddleware, middlewareTLS []websrv.HandlerMiddleware) {
	var promRegistration *websrv.PrometheusRegistration
	var err error
	promRegistration, err = websrv.AccessMetricsRegister(metrics.Registry, *metricsNamespace)
	if err != nil {
		log.Error().Err(err).Msg("Could not register custom prometheus metrics.")
	}
	middleware = []websrv.HandlerMiddleware{
		websrv.Optional(websrv.AccessMetrics(promRegistration), *accessLog),
		websrv.Optional(websrv.AccessLog(), *accessLog),
		chi.RequestID,
	}
	middlewareTLS = middleware
	headers := make(map[string]string)
	if *hstsEnabled {
		headers["Strict-Transport-Security"] = hstsConfig.hstsHeader()
	}
	altSvc := getAltSvcHeader()
	if altSvc != "" {
		headers["Alt-Svc"] = altSvc
	}
	middlewareTLS = append([]websrv.HandlerMiddleware{
		websrv.Header(headers),
	}, middlewareTLS...)
	return
}

// getAltSvcHeader returns the Alt-Svc HTTP-Header that advertises HTTP2 and HTTP3
func getAltSvcHeader() string {
	var sb strings.Builder
	if *http3Enabled && *http3AltSvcPort != 0 {
		sb.WriteString("h3=\":" + strconv.Itoa(*http3AltSvcPort) + "\", ")
	}
	if *http2AltSvcPort != 0 {
		sb.WriteString("h2=\":" + strconv.Itoa(*http2AltSvcPort) + "\", ")
	}
	if sb.Len() == 0 {
		return ""
	}
	return sb.String()[:sb.Len()-2]
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
func forwardUpdates(ctx context.Context, ingressReconciler *state.IngressReconciler, reverseProxy *revproxy.ReverseProxy) {
	for {
		select {
		case currentState := <-ingressReconciler.GetStateChan():
			err := reverseProxy.LoadIngressState(currentState)
			if err != nil {
				log.Error().Err(err).Msg("failed to apply updated currentState")
			}
		case <-ctx.Done():
			return
		}
	}
}
