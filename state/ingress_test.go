package state

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/networking/v1"
	v1Net "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var ingressClassName = "test"
var pathType = v1Net.PathTypePrefix

const debounceDuration = time.Duration(10) * time.Millisecond

func internalTestIngress(t *testing.T, setIngressPort func(*v1.Ingress)) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	stateManager := New(ctx, client, ingressClassName, DebounceDuration(debounceDuration))
	ingress := getDummyIngress()
	service := getDummyService()
	setIngressPort(ingress)

	_, err := client.CoreV1().Services(namespace).Create(ctx, service, v1Meta.CreateOptions{})
	assert.Nil(t, err)
	_, err = client.NetworkingV1().Ingresses(namespace).Create(ctx, ingress, v1Meta.CreateOptions{})
	assert.Nil(t, err)

	stateChan := stateManager.GetStateChan()
	state := <-stateChan
	backendPaths, ok := state.BackendPaths[host]
	assert.True(t, ok)
	assert.Equal(t, 1, len(backendPaths))
	backendPath := backendPaths[0]
	assert.Equal(t, namespace, backendPath.Namespace)
	assert.Equal(t, path, backendPath.Path)
	assert.Equal(t, serviceName, backendPath.ServiceName)
	assert.Equal(t, servicePort, backendPath.ServicePort)
}

func TestIngressServicePortNumber(t *testing.T) {
	setIngressPort := func(ingress *v1.Ingress) {
		ingress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.Service.Port.Number = servicePort
	}
	internalTestIngress(t, setIngressPort)
}

func TestIngressServicePortName(t *testing.T) {
	setIngressPort := func(ingress *v1.Ingress) {
		ingress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.Service.Port.Name = servicePortName
	}
	internalTestIngress(t, setIngressPort)
}

func TestSecret(t *testing.T) {
	ctx := context.Background()
	client := fake.NewSimpleClientset()
	stateManager := New(ctx, client, ingressClassName, DebounceDuration(debounceDuration))
	ingress := getDummyIngressSecretRef()
	secret, cert, certKey := getDummySecret(t)

	_, err := client.CoreV1().Secrets(namespace).Create(ctx, secret, v1Meta.CreateOptions{})
	assert.Nil(t, err)
	_, err = client.NetworkingV1().Ingresses(namespace).Create(ctx, ingress, v1Meta.CreateOptions{})
	assert.Nil(t, err)

	stateChan := stateManager.GetStateChan()
	state := <-stateChan
	loadedSecret, ok := state.TlsCerts[host]
	assert.True(t, ok)
	assert.Equal(t, cert, loadedSecret.Cert)
	assert.Equal(t, certKey, loadedSecret.Key)
}
