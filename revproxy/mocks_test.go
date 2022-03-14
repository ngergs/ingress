package revproxy

import (
	"bytes"
	"context"
	"net/http"

	"github.com/stretchr/testify/mock"
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
