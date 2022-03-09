package revproxy

import (
	"context"
	"crypto/tls"
	"net/http"
	"strconv"
	"time"

	"github.com/ngergs/ingress/state"
	websrv "github.com/ngergs/websrv/server"
	"github.com/rs/zerolog/log"
)

func New(ingressStateManager *state.IngressStateManager, options ...ConfigOption) (*ReverseProxy, error) {
	config := defaultConfig
	applyOptions(&config, options...)

	reverseProxy := &ReverseProxy{}
	state := <-ingressStateManager.GetStateChan()
	err := reverseProxy.LoadIngressState(state)
	return reverseProxy, err
}

func (proxy *ReverseProxy) ServeAndListen(ctx context.Context, handlerSetups ...websrv.HandlerMiddleware) chan error {
	errChan := make(chan error)
	proxy.startHttps(ctx, errChan, handlerSetups...)
	proxy.startHttp(ctx, errChan, handlerSetups...)
	return errChan
}

func (proxy *ReverseProxy) startHttps(ctx context.Context, errChan chan<- error, handlerSetups ...websrv.HandlerMiddleware) {
	tlsListener, err := tls.Listen("tcp", ":"+strconv.Itoa(proxy.Config.HttpsPort), proxy.TlsConfig())
	if err != nil {
		errChan <- err
		return
	}
	tlsHandler := proxy.GetHTTPSHandler()
	if proxy.Config.Hsts != nil {
		headerMiddleware := websrv.Header(&websrv.Config{Headers: map[string]string{"Strict-Transport-Security": proxy.Config.Hsts.hstsHeader()}})
		tlsHandler = headerMiddleware(tlsHandler)
	}
	tlsServer := &http.Server{
		Handler:      addMiddleware(tlsHandler, handlerSetups...),
		ReadTimeout:  time.Duration(10) * time.Second,
		WriteTimeout: time.Duration(10) * time.Second,
	}
	if err != nil {
		errChan <- err
		return
	}
	go func() {
		log.Info().Msgf("Listening for HTTPS under container port %d", proxy.Config.HttpsPort)
		errChan <- tlsServer.Serve(tlsListener)
	}()
	go proxy.gracefulShutdown(ctx, tlsServer)
}

func (proxy *ReverseProxy) startHttp(ctx context.Context, errChan chan<- error, handlerSetups ...websrv.HandlerMiddleware) {
	httpServer := &http.Server{
		Addr:         ":" + strconv.Itoa(proxy.Config.HttpPort),
		Handler:      addMiddleware(proxy.GetHTTPHandler(), handlerSetups...),
		ReadTimeout:  time.Duration(10) * time.Second,
		WriteTimeout: time.Duration(10) * time.Second,
	}
	go func() {
		log.Info().Msgf("Listening for HTTP under container port %d", proxy.Config.HttpPort)
		errChan <- httpServer.ListenAndServe()
	}()
	go proxy.gracefulShutdown(ctx, httpServer)
}

func (proxy *ReverseProxy) gracefulShutdown(ctx context.Context, server *http.Server) {
	for range ctx.Done() {
		shutdownCtx := context.Background()
		shutdownCtx, cancel := context.WithDeadline(shutdownCtx, time.Now().Add(proxy.Config.ShutdownTimeout))
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			log.Warn().Err(err).Msg("error durch graceful shutdown")
		}
		cancel()
		return
	}
}

func addMiddleware(root http.Handler, handlerSetups ...websrv.HandlerMiddleware) http.Handler {
	for _, handlerSetup := range handlerSetups {
		root = handlerSetup(root)
	}
	return root
}
