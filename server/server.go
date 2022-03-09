package server

import (
	"crypto/tls"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ngergs/ingress/state"
	websrv "github.com/ngergs/websrv/server"
	"github.com/rs/zerolog/log"
)

type Config struct {
	HttpPort              int
	HttpsPort             int
	HstsEnabled           bool
	HstsMaxAge            int
	HstsIncludeSubdomains bool
	HstsPreload           bool
}

func (hsts *Config) hstsHeader() string {
	if !hsts.HstsEnabled {
		return "max-age="
	}
	var result strings.Builder
	result.WriteString("max-age=")
	result.WriteString(strconv.Itoa(hsts.HstsMaxAge))
	if hsts.HstsIncludeSubdomains {
		result.WriteString("; includeSubDomains")
	}
	if hsts.HstsPreload {
		result.WriteString("; preload")
	}
	return result.String()
}

func Start(ingressStateManager *state.IngressStateManager, config *Config,
	errChan chan<- error, handlerSetups ...websrv.HandlerMiddleware) {
	state := <-ingressStateManager.GetStateChan()
	reverseProxyManager := reverseProxyManager{}
	err := reverseProxyManager.loadIngressState(state)
	if err != nil {
		errChan <- err
		return
	}

	startHttps(reverseProxyManager, config, errChan, handlerSetups...)
	startHttp(reverseProxyManager, config, errChan, handlerSetups...)

	go func() {
		for state := range ingressStateManager.GetStateChan() {
			err := reverseProxyManager.loadIngressState(state)
			if err != nil {
				log.Error().Err(err).Msg("failed to apply updated state")
			}
		}
	}()
}

func startHttps(reverseProxyManager reverseProxyManager, config *Config, errChan chan<- error, handlerSetups ...websrv.HandlerMiddleware) {
	tlsListener, err := tls.Listen("tcp", ":"+strconv.Itoa(config.HttpsPort), reverseProxyManager.tlsConfig())
	if err != nil {
		errChan <- err
		return
	}
	tlsHandler := reverseProxyManager.getHTTPSHandler()
	if config.HstsEnabled {
		headerMiddleware := websrv.Header(&websrv.Config{Headers: map[string]string{"Strict-Transport-Security": config.hstsHeader()}})
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
		log.Info().Msgf("Listening for HTTPS under container port %d", config.HttpsPort)
		errChan <- tlsServer.Serve(tlsListener)
	}()
}

func startHttp(reverseProxyManager reverseProxyManager, config *Config, errChan chan<- error, handlerSetups ...websrv.HandlerMiddleware) {
	httpServer := &http.Server{
		Addr:         ":" + strconv.Itoa(config.HttpPort),
		Handler:      addMiddleware(reverseProxyManager.getHTTPHandler(), handlerSetups...),
		ReadTimeout:  time.Duration(10) * time.Second,
		WriteTimeout: time.Duration(10) * time.Second,
	}
	go func() {
		log.Info().Msgf("Listening for HTTP under container port %d", config.HttpPort)
		errChan <- httpServer.ListenAndServe()
	}()
}

func addMiddleware(root http.Handler, handlerSetups ...websrv.HandlerMiddleware) http.Handler {
	for _, handlerSetup := range handlerSetups {
		root = handlerSetup(root)
	}
	return root
}
