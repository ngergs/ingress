package server

import (
	"crypto/tls"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ngergs/ingress/state"
	websrv "github.com/ngergs/websrv/server"
)

type HSTSconfig struct {
	Enabled           bool
	MaxAge            int
	IncludeSubdomains bool
	Preload           bool
}

func (hsts *HSTSconfig) header() string {
	if !hsts.Enabled {
		return "max-age="
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

func Start(ingressStateManager *state.IngressStateManager, httpPort int, httpsPort int,
	errChan chan<- error, hstsConfig *HSTSconfig, handlerSetups ...websrv.HandlerMiddleware) {
	state := <-ingressStateManager.GetStateChan()
	reverseProxyManager := reverseProxyManager{}
	err := reverseProxyManager.loadIngressState(state)
	if err != nil {
		errChan <- err
		return
	}

	listener, err := tls.Listen("tcp", ":"+strconv.Itoa(httpsPort), reverseProxyManager.tlsConfig())
	if err != nil {
		errChan <- err
		return
	}

	tlsHandler := reverseProxyManager.getTLSHandler()
	if hstsConfig.Enabled {
		headerMiddleware := websrv.Header(&websrv.Config{Headers: map[string]string{"Strict-Transport-Security": hstsConfig.header()}})
		tlsHandler = headerMiddleware(tlsHandler)
	}

	tlsServer := &http.Server{
		Handler:      addMiddleware(tlsHandler, handlerSetups...),
		ReadTimeout:  time.Duration(10) * time.Second,
		WriteTimeout: time.Duration(10) * time.Second,
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler), 0), // avoid weak TLS due to HTTP2
	}
	go func() { errChan <- tlsServer.Serve(listener) }()

	httpServer := &http.Server{
		Addr:         ":" + strconv.Itoa(httpPort),
		Handler:      addMiddleware(reverseProxyManager.getHTTPHandler(), handlerSetups...),
		ReadTimeout:  time.Duration(10) * time.Second,
		WriteTimeout: time.Duration(10) * time.Second,
	}
	go func() { errChan <- httpServer.ListenAndServe() }()

	go func() {
		for state := range ingressStateManager.GetStateChan() {
			err := reverseProxyManager.loadIngressState(state)
			if err != nil {
				errChan <- err
			}
		}
	}()
}

func addMiddleware(root http.Handler, handlerSetups ...websrv.HandlerMiddleware) http.Handler {
	for _, handlerSetup := range handlerSetups {
		root = handlerSetup(root)
	}
	return root
}
