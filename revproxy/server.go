package revproxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"

	"github.com/ngergs/ingress/state"
	websrv "github.com/ngergs/websrv/server"
	"github.com/rs/zerolog/log"
)

func New(ingressStateManager *state.IngressStateManager, options ...ConfigOption) *ReverseProxy {
	config := defaultConfig
	applyOptions(&config, options...)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout: config.BackendTimeout,
	}).DialContext
	reverseProxy := &ReverseProxy{Config: &config, Transport: transport}
	return reverseProxy
}

func (proxy *ReverseProxy) GetServerHttps(ctx context.Context, handlerSetups ...websrv.HandlerMiddleware) (*http.Server, net.Listener, error) {
	tlsListener, err := tls.Listen("tcp", ":"+strconv.Itoa(proxy.Config.HttpsPort), proxy.TlsConfig())
	if err != nil {
		return nil, nil, err
	}
	tlsHandler := proxy.GetHTTPSHandler()
	if proxy.Config.Hsts != nil {
		headerMiddleware := websrv.Header(&websrv.Config{Headers: map[string]string{"Strict-Transport-Security": proxy.Config.Hsts.hstsHeader()}})
		tlsHandler = headerMiddleware(tlsHandler)
	}
	tlsServer := &http.Server{
		Handler:      addMiddleware(tlsHandler, handlerSetups...),
		ReadTimeout:  proxy.Config.ReadTimeout,
		WriteTimeout: proxy.Config.WriteTimeout,
	}
	if err != nil {
		return nil, nil, err
	}
	log.Info().Msgf("Listening for HTTPS under container port %d", proxy.Config.HttpsPort)
	return tlsServer, tlsListener, nil
}

func (proxy *ReverseProxy) GetServerHttp(ctx context.Context, handlerSetups ...websrv.HandlerMiddleware) (*http.Server, error) {
	httpServer := &http.Server{
		Addr:         ":" + strconv.Itoa(proxy.Config.HttpPort),
		Handler:      addMiddleware(proxy.GetHTTPHandler(), handlerSetups...),
		ReadTimeout:  proxy.Config.ReadTimeout,
		WriteTimeout: proxy.Config.WriteTimeout,
	}
	log.Info().Msgf("Listening for HTTP under container port %d", proxy.Config.HttpPort)
	return httpServer, nil
}

func addMiddleware(root http.Handler, handlerSetups ...websrv.HandlerMiddleware) http.Handler {
	for _, handlerSetup := range handlerSetups {
		root = handlerSetup(root)
	}
	return root
}
