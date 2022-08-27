package revproxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	v1Net "k8s.io/api/networking/v1"
)

const acmePath = "/.well-known/acme-challenge"

// ReverseProxy implements the main ingress reverse proxy logic
type ReverseProxy struct {
	// state holds the internal current state of the reverse proxy. Changes when a new config is loaded via the LoadIngressState method.
	state atomic.Pointer[reverseProxyState]
	// Transport are the transport configurations for the reverse proxy. Will be cloned for each path.
	Transport *http.Transport
}

// BackendRouting contains a mopping of host name to the relevant backend path handlers in order of priority
type BackendRouting map[string]backendPathHandlers

// TlsCerts contains a mapping of host name to the relevant TLS certificates
type TlsCerts map[string]*tls.Certificate

// reverseProxyState holds the current state of the reverse proxy.
type reverseProxyState struct {
	backendPathHandlers BackendRouting
	tlsCerts            TlsCerts
}

// backendPathHandlers is a slice of backendPathHandler
type backendPathHandlers []*backendPathHandler

// backendPathHandler holds the ingress PathRule for path matching as well as the corresponding reverse proxy handler for the given backend path.
type backendPathHandler struct {
	ProxyHandler http.Handler
	PathType     *v1Net.PathType
	Path         string
}

// match returns the matching backendPathHandler for the given path argument if one is present
func (pathHandlers *backendPathHandlers) match(path string) (pathHandler *backendPathHandler, ok bool) {
	for _, pathHandler := range *pathHandlers {
		if *pathHandler.PathType == v1Net.PathTypeExact && path == pathHandler.Path {
			return pathHandler, true
		}
		if strings.HasPrefix(path, pathHandler.Path) {
			return pathHandler, true
		}
	}
	return nil, false
}

// New setups a new reverse proxy. To start it see methods GetServerHttp and GetServerHttps.
func New(options ...ConfigOption) *ReverseProxy {
	config := defaultConfig.clone().applyOptions(options...)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout: config.BackendTimeout,
	}).DialContext
	reverseProxy := &ReverseProxy{Transport: transport}
	return reverseProxy
}

// GetCertificateFunc returns a function for the tls.Config.GetCertificate callback.
// Supposed to be used with tls.Listener.
func (proxy *ReverseProxy) GetCertificateFunc() func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		state := proxy.state.Load()
		if state == nil {
			return nil, fmt.Errorf("state not initialized")
		}
		cert, ok := state.tlsCerts[hello.ServerName]

		if !ok {
			return nil, fmt.Errorf("no certificate found for servername %s", hello.ServerName)
		}
		return cert, nil
	}
}

// GetHandlerProxying returns the main proxying handler. Can be used with HTTP and HTTPS listeners.
// A TLS-terminating setup should use this for HTTPS only.
func (proxy *ReverseProxy) GetHandlerProxying() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := proxy.state.Load()
		if state == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		pathHandlers, ok := state.backendPathHandlers[r.Host]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return // no response if host does not match
		}
		// first match is selected
		pathHandler, ok := pathHandlers.match(r.URL.Path)
		if ok {
			pathHandler.ProxyHandler.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
}

// GetHttpsRedirectHandler returns a handler which redirects all requests with HTTP status 308 to the same route but with the https scheme.
// Should therefore not be used for TLS listeners.
// Paths that start with  "/.well-known/acme-challenge" are stil reverse proxied to the backend for ACME challenges.
func (proxy *ReverseProxy) GetHttpsRedirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := proxy.state.Load()
		if state == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		pathHandlers, ok := state.backendPathHandlers[r.Host]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, acmePath) {
			pathHandler, ok := pathHandlers.match(r.URL.Path)
			if ok {
				pathHandler.ProxyHandler.ServeHTTP(w, r)
				return
			}
		}
		_, ok = pathHandlers.match(r.URL.Path)
		if ok {
			w.Header().Set("Location", "https://"+r.Host+r.URL.Path)
			w.WriteHeader(http.StatusPermanentRedirect)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
}
