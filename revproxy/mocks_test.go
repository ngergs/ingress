package revproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	v1Net "k8s.io/api/networking/v1"
)

const dummyHost = "localhost"
const prefixPath = "/test"

type mockResponseWriter struct {
	mock         mock.Mock
	receivedData bytes.Buffer
}

func (w *mockResponseWriter) Header() http.Header {
	args := w.mock.Called()
	return args.Get(0).(http.Header)
}

func (w *mockResponseWriter) Write(data []byte) (int, error) {
	return w.receivedData.Write(data)
}

func (w *mockResponseWriter) WriteHeader(statusCode int) {
	w.mock.Called(statusCode)
}

type mockHandler struct {
	w             http.ResponseWriter
	r             *http.Request
	serveHttpFunc func(w http.ResponseWriter, r *http.Request)
}

func (handler *mockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handler.w = w
	handler.r = r
	if handler.serveHttpFunc != nil {
		handler.serveHttpFunc(w, r)
	}
}

// getDefaultHandlerMocks provides default mocks used for handler testing
func getDefaultHandlerMocks() (w *mockResponseWriter, r *http.Request, next *mockHandler) {
	next = &mockHandler{}
	w = &mockResponseWriter{}
	r = &http.Request{Header: make(map[string][]string)}
	r = r.WithContext(context.Background())
	return
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
