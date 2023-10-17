package revproxy

import (
	"crypto/tls"
	"github.com/ngergs/ingress/state"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"

	"github.com/rs/zerolog/log"
	v1Net "k8s.io/api/networking/v1"
)

// LoadIngressState loads a new ingress state as reverse proxy settings.
// There is no downtime during this change. The new state is processed and then swapped in
// while supporting concurrent requests.
// Once applied the reverse proxy is then purely defined by the new state.
func (proxy *ReverseProxy) LoadIngressState(state state.IngressState) error {
	backendPathHandlers, err := getBackendPathHandlers(state, proxy.Transport)
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
	log.Info().Msg("Reverse proxy state updated")
	return nil
}

// getBackendPathHandlers is an internal function which evaluates the ingress state and collects the path rules from it.
// Furthermore, also the relevant reverse proxy clients are already setup.
// Paths are matched based on the principle that exact matches take prevalence over prefix matches.
// If no exact match has been found the longest matching prefix path takes prevalence.
func getBackendPathHandlers(state state.IngressState, backendTransport http.RoundTripper) (BackendRouting, error) {
	pathHandlerMap := make(BackendRouting)
	for host, domainConfig := range state {
		proxies := make([]*backendPathHandler, len(domainConfig.BackendPaths))
		for i, pathRule := range domainConfig.BackendPaths {

			rawUrl := "http://" + pathRule.ServiceName +
				"." + pathRule.Namespace +
				".svc.cluster.local" +
				":" + strconv.FormatInt(int64(pathRule.ServicePort), 10)
			url, err := url.ParseRequestURI(rawUrl)
			if err != nil {
				return nil, err
			}
			log.Info().Msgf("Loaded proxy backend path %s for host %s and path %s", url.String(), host, pathRule.Path)

			revProxy := httputil.NewSingleHostReverseProxy(url)
			revProxy.Transport = backendTransport
			proxies[i] = &backendPathHandler{
				PathType:     pathRule.PathType,
				Path:         pathRule.Path,
				ProxyHandler: revProxy,
			}
		}
		// exact type match first, then the longest path
		sort.Slice(proxies, func(i int, j int) bool {
			if *proxies[i].PathType == v1Net.PathTypeExact {
				return true
			}
			if *proxies[j].PathType == v1Net.PathTypeExact {
				return false
			}
			return len(proxies[i].Path) > len(proxies[j].Path)
		})
		pathHandlerMap[host] = proxies
	}
	return pathHandlerMap, nil
}

// getTlsCerts is an internal function which collects the relevant tls-secrets
// and also loads the certificates.
func getTlsCerts(state state.IngressState) (TlsCerts, error) {
	tlsCerts := make(map[string]*tls.Certificate)
	for host, domainConfig := range state {
		if domainConfig.TlsCert == nil {
			continue
		}
		cert, err := tls.X509KeyPair(domainConfig.TlsCert.Cert, domainConfig.TlsCert.Key)
		if err != nil {
			return nil, err
		}
		log.Info().Msgf("Loaded certificate for host %s", host)
		tlsCerts[host] = &cert
	}
	return tlsCerts, nil
}
