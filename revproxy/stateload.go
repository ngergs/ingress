package revproxy

import (
	"crypto/tls"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"

	"github.com/ngergs/ingress/state"
	"github.com/rs/zerolog/log"
	v1Net "k8s.io/api/networking/v1"
)

func getBackendPathHandlers(state *state.IngressState) (BackendRouting, error) {
	pathHandlerMap := make(BackendRouting)
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

func getTlsCerts(state *state.IngressState) (TlsCerts, error) {
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
