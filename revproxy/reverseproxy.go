package revproxy

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/ngergs/ingress/state"
	v1Net "k8s.io/api/networking/v1"
)

type ReverseProxy struct {
	Config *Config
	state  atomic.Value // *reverseProxyState
}

type BackendRouting map[string][]*backendPathHandler //host->paths in order of priority
type TlsCerts map[string]*tls.Certificate            //host->cert

type reverseProxyState struct {
	backendPathHandlers BackendRouting
	tlsCerts            TlsCerts
}

type backendPathHandler struct {
	PathRule     *state.IngressPathConfig
	ProxyHandler http.Handler
}

func (proxy *ReverseProxy) getState() (state *reverseProxyState, ok bool) {
	result := proxy.state.Load()
	if result == nil {
		return nil, false
	}
	return result.(*reverseProxyState), true
}

func (proxy *ReverseProxy) LoadIngressState(state *state.IngressState) error {
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

func (proxy *ReverseProxy) TlsConfig() *tls.Config {
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

func (proxy *ReverseProxy) GetHTTPSHandler() http.Handler {
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

func (proxy *ReverseProxy) GetHTTPHandler() http.Handler {
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

// matches returns if the path satisfies the pathRules. The ImplementationSpecific PathType is evaluated as Prefix PathType.
func matches(path string, pathRule *state.IngressPathConfig) bool {
	if *pathRule.Config.PathType == v1Net.PathTypeExact {
		return path == pathRule.Config.Path
	}
	return strings.HasPrefix(path, pathRule.Config.Path)
}
