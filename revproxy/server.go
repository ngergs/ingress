package revproxy

import (
	"context"
	"net"
	"net/http"
	"strconv"

	websrv "github.com/ngergs/websrv/server"
	"github.com/rs/zerolog/log"
)

// New setups a new reverse proxy. To start it see methods GetServerHttp and GetServerHttps.
func New(options ...ConfigOption) *ReverseProxy {
	config := defaultConfig.clone()
	config.applyOptions(options...)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout: config.BackendTimeout,
	}).DialContext
	reverseProxy := &ReverseProxy{Config: config, Transport: transport}
	return reverseProxy
}

// GetServerTLS returns the http.Serverto start the https endpoint.
// For certificate matching to work correctly the server has to use the net.Listener from tls.Listen with the tls.Config from the TlsConfig method.
// Therefore the port is also nto set here.
// Middleware is applied in order of occurence, i.e. the first provided middleare sees the request first.
func (proxy *ReverseProxy) GetServerTLS(ctx context.Context, handlerSetups ...websrv.HandlerMiddleware) *http.Server {
	tlsHandler := proxy.GetHandlerProxying()
	return &http.Server{
		Handler:      addMiddleware(tlsHandler, handlerSetups...),
		ReadTimeout:  proxy.Config.ReadTimeout,
		WriteTimeout: proxy.Config.WriteTimeout,
	}
}

// GetServer returns the http.Server to start the http endpoint.
// Middleware is applied in order of occurence, i.e. the first provided middleare sees the request first.
func (proxy *ReverseProxy) GetServer(ctx context.Context, port int, handlerSetups ...websrv.HandlerMiddleware) *http.Server {
	log.Info().Msgf("Listening for HTTP under container port %d", port)
	return &http.Server{
		Addr:         ":" + strconv.Itoa(port),
		Handler:      addMiddleware(proxy.GetHttpsRedirectHandler(), handlerSetups...),
		ReadTimeout:  proxy.Config.ReadTimeout,
		WriteTimeout: proxy.Config.WriteTimeout,
	}
}

// addMiddleware is an internal function to apply functional middleware wrapper to a root http.Handler.
func addMiddleware(root http.Handler, handlerSetups ...websrv.HandlerMiddleware) http.Handler {
	for _, handlerSetup := range handlerSetups {
		root = handlerSetup(root)
	}
	return root
}
