package revproxy

import (
	"crypto/tls"
	"io/ioutil"
	"testing"

	"github.com/ngergs/ingress/state"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/networking/v1"
)

func TestLoadIngressState(t *testing.T) {
	state, cert := getValidDummyState(t)
	reverseProxy := New()
	err := reverseProxy.LoadIngressState(state)
	assert.Nil(t, err)
	proxyState := reverseProxy.state.Load().(*reverseProxyState)

	assert.Equal(t, cert, proxyState.tlsCerts[dummyHost])

	// expectedOrder in proxystate is 2->0->1 as exact paths take precedence over prefixes and the longest prefixes wins against other prefixes
	assertEqual(t, state.BackendPaths[dummyHost][0], proxyState.backendPathHandlers[dummyHost][2])
	assertEqual(t, state.BackendPaths[dummyHost][1], proxyState.backendPathHandlers[dummyHost][0])
	assertEqual(t, state.BackendPaths[dummyHost][2], proxyState.backendPathHandlers[dummyHost][1])
}

func TestLoadIngressStateCertError(t *testing.T) {
	state := getDummyState(nil, nil)
	reverseProxy := New()
	err := reverseProxy.LoadIngressState(state)
	assert.NotNil(t, err)
}

func assertEqual(t *testing.T, backendPath *state.PathConfig, proxyBackendPath *backendPathHandler) {
	assert.Equal(t, backendPath.PathType, proxyBackendPath.PathType)
	assert.Equal(t, backendPath.Path, proxyBackendPath.Path)
}

func getValidDummyState(t *testing.T) (*state.IngressState, *tls.Certificate) {
	cert, err := tls.LoadX509KeyPair("../test/cert.pem", "../test/key.pem")
	assert.Nil(t, err)
	certData, err := ioutil.ReadFile("../test/cert.pem")
	assert.Nil(t, err)
	certKey, err := ioutil.ReadFile("../test/key.pem")
	assert.Nil(t, err)
	return getDummyState(certData, certKey), &cert
}

func getDummyState(cert []byte, certKey []byte) *state.IngressState {
	exact := v1.PathTypeExact
	prefix := v1.PathTypePrefix
	backendPaths := state.BackendPaths{
		dummyHost: {
			&state.PathConfig{
				PathType: &prefix,
				Path:     "/",
			},
			&state.PathConfig{
				PathType: &exact,
				Path:     "/test123",
			},
			&state.PathConfig{
				PathType: &prefix,
				Path:     "/test",
			},
		},
	}

	tlsCert := &state.TlsCert{
		Cert: cert,
		Key:  certKey,
	}
	tlsCerts := state.TlsCerts{
		dummyHost: tlsCert,
	}
	return &state.IngressState{
		BackendPaths: backendPaths,
		TlsCerts:     tlsCerts,
	}
}
