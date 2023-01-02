package state

import (
	"context"
	"github.com/stretchr/testify/assert"
	v1Net "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"testing"
)

var ingressClassName = "test"
var pathType = v1Net.PathTypePrefix

func internalTestIngress(t *testing.T, setIngressPort func(*v1Net.Ingress)) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	ingress := getDummyIngress()
	service := getDummyService()
	setIngressPort(ingress)
	_, err := client.CoreV1().Services(namespace).Create(ctx, service, v1Meta.CreateOptions{})
	assert.NoError(t, err)
	_, err = client.NetworkingV1().Ingresses(namespace).Create(ctx, ingress, v1Meta.CreateOptions{})
	assert.NoError(t, err)

	stateManager, err := New(ctx, client, ingressClassName, nil)
	assert.NoError(t, err)
	stateChan := stateManager.GetStateChan()
	state := <-stateChan
	domainConfig, ok := state[host]
	assert.True(t, ok)
	assert.Equal(t, 1, len(domainConfig.BackendPaths))
	backendPath := domainConfig.BackendPaths[0]
	assert.Equal(t, namespace, backendPath.Namespace)
	assert.Equal(t, path, backendPath.Path)
	assert.Equal(t, serviceName, backendPath.ServiceName)
	assert.Equal(t, servicePort, backendPath.ServicePort)
}

func TestIngressServicePortNumber(t *testing.T) {
	setIngressPort := func(ingress *v1Net.Ingress) {
		ingress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.Service.Port.Number = servicePort
	}
	internalTestIngress(t, setIngressPort)
}

func TestIngressServicePortName(t *testing.T) {
	setIngressPort := func(ingress *v1Net.Ingress) {
		ingress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.Service.Port.Name = servicePortName
	}
	internalTestIngress(t, setIngressPort)
}

func TestSecret(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	ingress := getDummyIngressSecretRef()
	secret, cert, certKey := getDummySecret(t)

	_, err := client.CoreV1().Secrets(namespace).Create(ctx, secret, v1Meta.CreateOptions{})
	assert.NoError(t, err)
	_, err = client.NetworkingV1().Ingresses(namespace).Create(ctx, ingress, v1Meta.CreateOptions{})
	assert.NoError(t, err)

	stateManager, err := New(ctx, client, ingressClassName, nil)
	assert.NoError(t, err)
	stateChan := stateManager.GetStateChan()
	state := <-stateChan
	domainConfig, ok := state[host]
	assert.True(t, ok)
	assert.Equal(t, cert, domainConfig.TlsCert.Cert)
	assert.Equal(t, certKey, domainConfig.TlsCert.Key)
}
