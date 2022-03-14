package revproxy

import (
	"crypto/rand"
	"crypto/tls"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	v1Net "k8s.io/api/networking/v1"
)

func TestTlsConfigMatch(t *testing.T) {
	reverseProxy := getDummyReverseProxy(t, nil)
	expectedCert := reverseProxy.state.Load().(*reverseProxyState).tlsCerts[dummyHost]
	receivedCert, err := reverseProxy.TlsConfig().GetCertificate(&tls.ClientHelloInfo{
		ServerName: dummyHost,
	})
	assert.Nil(t, err)
	assert.Equal(t, expectedCert, receivedCert)
}

func TestTlsConfigMissMatch(t *testing.T) {
	reverseProxy := getDummyReverseProxy(t, nil)
	_, err := reverseProxy.TlsConfig().GetCertificate(&tls.ClientHelloInfo{
		ServerName: "none",
	})
	assert.NotNil(t, err)
}

func TestTlsConfigStateNotRdy(t *testing.T) {
	reverseProxy := &ReverseProxy{}
	_, err := reverseProxy.TlsConfig().GetCertificate(&tls.ClientHelloInfo{
		ServerName: dummyHost,
	})
	assert.NotNil(t, err)
}

func internalTestHandlerProxying(t *testing.T, host string, path string, expectedStatus int) {
	w, r, next := getDefaultHandlerMocks()
	next.serveHttpFunc = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	reverseProxy := getDummyReverseProxy(t, next)
	handler := reverseProxy.GetHandlerProxying()
	w.mock.On("WriteHeader", expectedStatus).Return()
	r.Host = host
	r.URL = &url.URL{Path: path}
	handler.ServeHTTP(w, r)
	w.mock.AssertExpectations(t)
}

func TestHandlerProxying(t *testing.T) {
	internalTestHandlerProxying(t, "none", "/", http.StatusNotFound)
	internalTestHandlerProxying(t, dummyHost, "/", http.StatusNotFound)
	internalTestHandlerProxying(t, dummyHost, prefixPath, http.StatusOK)
	internalTestHandlerProxying(t, dummyHost, prefixPath+"/sub", http.StatusOK)
}

func internalTestHandlerRedirecting(t *testing.T, host string, path string, expectedStatus int) {
	w, r, next := getDefaultHandlerMocks()
	next.serveHttpFunc = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	reverseProxy := getDummyReverseProxy(t, next)
	handler := reverseProxy.GetHttpsRedirectHandler()
	headers := make(http.Header)
	w.mock.On("WriteHeader", expectedStatus).Return()
	if expectedStatus == http.StatusPermanentRedirect {
		w.mock.On("Header").Return(headers)
	}
	r.Host = host
	r.URL = &url.URL{Path: path}
	handler.ServeHTTP(w, r)
	w.mock.AssertExpectations(t)
	if expectedStatus == http.StatusPermanentRedirect {
		location := headers.Get("Location")
		assert.Equal(t, "https://"+host+path, location)
	}
}

func TestHandlerRedirecting(t *testing.T) {
	internalTestHandlerRedirecting(t, "none", "/", http.StatusNotFound)
	internalTestHandlerRedirecting(t, dummyHost, "/", http.StatusNotFound)
	internalTestHandlerRedirecting(t, dummyHost, prefixPath, http.StatusPermanentRedirect)
	internalTestHandlerRedirecting(t, dummyHost, prefixPath+"/sub", http.StatusPermanentRedirect)
	internalTestHandlerRedirecting(t, "none", acmePath, http.StatusNotFound)
	internalTestHandlerRedirecting(t, dummyHost, acmePath, http.StatusOK)
}

func internalTestHandlerStateNotRdy(t *testing.T, handler http.Handler) {
	w, r, _ := getDefaultHandlerMocks()
	w.mock.On("WriteHeader", http.StatusServiceUnavailable).Return()
	handler.ServeHTTP(w, r)
	w.mock.AssertExpectations(t)
}

func TestHandlerStateNotRdy(t *testing.T) {
	reverseProxy := &ReverseProxy{}
	internalTestHandlerStateNotRdy(t, reverseProxy.GetHandlerProxying())
	internalTestHandlerStateNotRdy(t, reverseProxy.GetHttpsRedirectHandler())
}

func getDummyReverseProxy(t *testing.T, handler http.Handler) *ReverseProxy {
	pathType := v1Net.PathTypePrefix
	exact := v1Net.PathTypeExact
	pathHandler := &backendPathHandler{
		PathType:     &pathType,
		Path:         prefixPath,
		ProxyHandler: handler,
	}
	acmeHandler := &backendPathHandler{
		PathType:     &exact,
		Path:         acmePath,
		ProxyHandler: handler,
	}
	pathMap := map[string]backendPathHandlers{
		dummyHost: {pathHandler, acmeHandler},
	}

	var certData [20]byte
	_, err := rand.Read(certData[:])
	assert.Nil(t, err)
	cert := tls.Certificate{
		Certificate: [][]byte{certData[:]},
	}
	certMap := map[string]*tls.Certificate{
		dummyHost: &cert,
	}

	reverseProxy := New()
	reverseProxy.state.Store(&reverseProxyState{
		backendPathHandlers: pathMap,
		tlsCerts:            certMap,
	})
	return reverseProxy
}
