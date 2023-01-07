package revproxy

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTlsConfigMatch(t *testing.T) {
	reverseProxy := getDummyReverseProxy(t, nil)
	state := reverseProxy.state.Load()
	require.NotNil(t, state)
	expectedCert := state.tlsCerts[dummyHost]
	receivedCert, err := reverseProxy.GetCertificateFunc()(&tls.ClientHelloInfo{
		ServerName: dummyHost,
	})
	require.Nil(t, err)
	require.Equal(t, expectedCert, receivedCert)
}

func TestTlsConfigMissMatch(t *testing.T) {
	reverseProxy := getDummyReverseProxy(t, nil)
	_, err := reverseProxy.GetCertificateFunc()(&tls.ClientHelloInfo{
		ServerName: "none",
	})
	require.NotNil(t, err)
}

func TestTlsConfigStateNotRdy(t *testing.T) {
	reverseProxy := &ReverseProxy{}
	_, err := reverseProxy.GetCertificateFunc()(&tls.ClientHelloInfo{
		ServerName: dummyHost,
	})
	require.NotNil(t, err)
}

func internalTestHandlerProxying(t *testing.T, host string, path string, expectedStatus int) {
	w, r, next := getDefaultHandlerMocks()
	next.serveHttpFunc = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	reverseProxy := getDummyReverseProxy(t, next)
	handler := reverseProxy.GetHandlerProxying()
	r.Host = host
	r.URL = &url.URL{Path: path}
	handler.ServeHTTP(w, r)
	require.Equal(t, expectedStatus, w.Result().StatusCode)
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
	r.Host = host
	r.URL = &url.URL{Path: path}
	handler.ServeHTTP(w, r)
	require.Equal(t, expectedStatus, w.Result().StatusCode)
	if expectedStatus == http.StatusPermanentRedirect {
		location := w.Result().Header.Get("Location")
		require.Equal(t, "https://"+host+path, location)
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
	handler.ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Result().StatusCode)
}

func TestHandlerStateNotRdy(t *testing.T) {
	reverseProxy := &ReverseProxy{}
	internalTestHandlerStateNotRdy(t, reverseProxy.GetHandlerProxying())
	internalTestHandlerStateNotRdy(t, reverseProxy.GetHttpsRedirectHandler())
}
