package server

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/ngergs/ingress/state"
	"github.com/rs/zerolog/log"
	v1Net "k8s.io/api/networking/v1"
)

type reverseProxyManager struct {
	state atomic.Value // *reverseProxyState
}

type reverseProxyState struct {
	backendPathHandlers map[string][]*backendPathHandler
	tlsCerts            map[string]*tls.Certificate
}

type backendPathHandler struct {
	PathRule     *state.IngressPathConfig
	ProxyHandler http.Handler
}

func (proxy *reverseProxyManager) getState() (state *reverseProxyState, ok bool) {
	result := proxy.state.Load()
	if result == nil {
		return nil, false
	}
	return result.(*reverseProxyState), true
}

func (proxy *reverseProxyManager) loadIngressState(state *state.IngressState) error {
	backendPathHandlers, err := getBackendPathHandlers(state)
	if err != nil {
		return err
	}
	tlsCerts, err := getTlsCerts(state)
	if err != nil {
		return err
	}
	newProxyState := &reverseProxyState{
		backendPathHandlers: backendPathHandlers,
		tlsCerts:            tlsCerts,
	}
	proxy.state.Store(newProxyState)
	return nil
}

func (proxy *reverseProxyManager) tlsConfig() *tls.Config {
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		},
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			state, ok := proxy.getState()
			if !ok {
				return nil, fmt.Errorf("state not initialized")
			}
			cert, ok := state.tlsCerts[hello.ServerName]
			if !ok {
				return nil, fmt.Errorf("no certificate found for servername %s", hello.ServerName)
			}
			return cert, nil
		},
	}
}

func (proxy *reverseProxyManager) getHTTPSHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, ok := proxy.getState()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		pathHandlers, ok := state.backendPathHandlers[r.Host]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return // no response if host does not match
		}
		// first match is selected
		for _, pathHandler := range pathHandlers {
			if matches(r.URL.Path, pathHandler.PathRule) {
				pathHandler.ProxyHandler.ServeHTTP(w, r)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
}

func (proxy *reverseProxyManager) getHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state, ok := proxy.getState()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		pathHandlers, ok := state.backendPathHandlers[r.Host]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge") {
			for _, pathHandler := range pathHandlers {
				if matches(r.URL.Path, pathHandler.PathRule) {
					pathHandler.ProxyHandler.ServeHTTP(w, r)
					return
				}
			}
		}
		w.Header().Set("Location", "https://"+r.Host+r.URL.Path)
		w.WriteHeader(http.StatusPermanentRedirect)
	})
}

func getBackendPathHandlers(state *state.IngressState) (map[string][]*backendPathHandler, error) {
	pathHandlerMap := make(map[string][]*backendPathHandler)
	for host, pathRules := range state.PathMap {
		proxies := make([]*backendPathHandler, len(pathRules))
		for i, pathRule := range pathRules {

			rawUrl := "http://" + pathRule.Config.Backend.Service.Name +
				"." + pathRule.Namespace +
				".svc.cluster.local" +
				":" + strconv.FormatInt(int64(pathRule.ServicePort.Port), 10)
			url, err := url.ParseRequestURI(rawUrl)
			if err != nil {
				return nil, err
			}
			log.Info().Msgf("Loaded proxy backend path %s for host %s and path %s", url.String(), host, pathRule.Config.Path)

			proxies[i] = &backendPathHandler{
				PathRule:     pathRule,
				ProxyHandler: httputil.NewSingleHostReverseProxy(url),
			}
		}
		// exact type match first, then the longest path
		sort.Slice(proxies, func(i int, j int) bool {
			if *proxies[i].PathRule.Config.PathType == v1Net.PathTypeExact {
				return true
			}
			if *proxies[j].PathRule.Config.PathType == v1Net.PathTypeExact {
				return false
			}
			return len(proxies[i].PathRule.Config.Path) > len(proxies[j].PathRule.Config.Path)
		})
		pathHandlerMap[host] = proxies
	}
	return pathHandlerMap, nil
}

func getTlsCerts(state *state.IngressState) (map[string]*tls.Certificate, error) {
	tlsCerts := make(map[string]*tls.Certificate)
	for host, secret := range state.TlsSecrets {
		cert, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
		if err != nil {
			return nil, err
		}
		log.Info().Msgf("Loaded certificte for host %s", host)
		tlsCerts[host] = &cert
	}
	return tlsCerts, nil
}

func matches(path string, pathRule *state.IngressPathConfig) bool {
	if *pathRule.Config.PathType == v1Net.PathTypeExact {
		return path == pathRule.Config.Path
	}
	// Prefix Matching is our default
	return strings.HasPrefix(path, pathRule.Config.Path)
}
