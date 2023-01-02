package revproxy

import (
	"crypto/tls"
	"github.com/ngergs/ingress/state"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/networking/v1"
)

func TestLoadIngressState(t *testing.T) {
	inputState, cert := getValidDummyState(t)
	reverseProxy := New()
	err := reverseProxy.LoadIngressState(inputState)
	assert.Nil(t, err)
	proxyState := reverseProxy.state.Load()
	assert.NotNil(t, proxyState)
	assert.Equal(t, cert, proxyState.tlsCerts[dummyHost])

	// expectedOrder in proxyState is 2->0->1 as exact paths take precedence over prefixes and the longest prefixes wins against other prefixes
	assertPathEqual(t, inputState[dummyHost].BackendPaths[0], proxyState.backendPathHandlers[dummyHost][2])
	assertPathEqual(t, inputState[dummyHost].BackendPaths[1], proxyState.backendPathHandlers[dummyHost][0])
	assertPathEqual(t, inputState[dummyHost].BackendPaths[2], proxyState.backendPathHandlers[dummyHost][1])
}

func TestLoadIngressStateCertError(t *testing.T) {
	inputState := getDummyState(nil, nil)
	reverseProxy := New()
	err := reverseProxy.LoadIngressState(inputState)
	assert.NotNil(t, err)
}

func assertPathEqual(t *testing.T, backendPath *state.BackendPath, proxyBackendPath *backendPathHandler) {
	assert.Equal(t, backendPath.PathType, proxyBackendPath.PathType)
	assert.Equal(t, backendPath.Path, proxyBackendPath.Path)
}

func getValidDummyState(t *testing.T) (state.IngressState, *tls.Certificate) {
	cert, err := tls.LoadX509KeyPair("../test/cert.pem", "../test/key.pem")
	assert.Nil(t, err)
	certData, err := os.ReadFile("../test/cert.pem")
	assert.Nil(t, err)
	certKey, err := os.ReadFile("../test/key.pem")
	assert.Nil(t, err)
	return getDummyState(certData, certKey), &cert
}

func getDummyState(cert []byte, certKey []byte) state.IngressState {
	exact := v1.PathTypeExact
	prefix := v1.PathTypePrefix
	backendPaths := []*state.BackendPath{
		{
			PathType: &prefix,
			Path:     "/",
		},
		{
			PathType: &exact,
			Path:     "/test123",
		},
		{
			PathType: &prefix,
			Path:     "/test",
		},
	}

	tlsCert := &state.TlsCert{
		Cert: cert,
		Key:  certKey,
	}
	return state.IngressState{
		dummyHost: {
			BackendPaths: backendPaths,
			TlsCert:      tlsCert,
		},
	}
}
