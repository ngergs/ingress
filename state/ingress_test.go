package state

import (
	"context"
	"github.com/stretchr/testify/require"
	v1Net "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sync"
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
	require.NoError(t, err)
	_, err = client.NetworkingV1().Ingresses(namespace).Create(ctx, ingress, v1Meta.CreateOptions{})
	require.NoError(t, err)

	stateReconciler := &IngressReconciler{
		ingressClassName:          ingressClassName,
		ingressState:              make(map[types.NamespacedName]*v1Net.Ingress),
		ingressProcessedStateChan: make(chan IngressState),
		k8sClients:                newKubernetesClients(client)}
	err = stateReconciler.k8sClients.startInformers(ctx)
	require.NoError(t, err)
	stateReconciler.k8sClients.waitForSync(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// have to run this in parallel as stateChan has no cache
		result, err := stateReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: ingress.Name}})
		require.NoError(t, err)
		require.False(t, result.Requeue)
	}()
	stateChan := stateReconciler.GetStateChan()
	state := <-stateChan
	wg.Wait()
	domainConfig, ok := state[host]
	require.True(t, ok)
	require.Equal(t, 1, len(domainConfig.BackendPaths))
	backendPath := domainConfig.BackendPaths[0]
	require.Equal(t, namespace, backendPath.Namespace)
	require.Equal(t, path, backendPath.Path)
	require.Equal(t, serviceName, backendPath.ServiceName)
	require.Equal(t, servicePort, backendPath.ServicePort)
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
	require.NoError(t, err)
	_, err = client.NetworkingV1().Ingresses(namespace).Create(ctx, ingress, v1Meta.CreateOptions{})
	require.NoError(t, err)

	stateReconciler := &IngressReconciler{
		ingressClassName:          ingressClassName,
		ingressState:              make(map[types.NamespacedName]*v1Net.Ingress),
		ingressProcessedStateChan: make(chan IngressState),
		k8sClients:                newKubernetesClients(client)}
	err = stateReconciler.k8sClients.startInformers(ctx)
	require.NoError(t, err)
	stateReconciler.k8sClients.waitForSync(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// have to run this in parallel as stateChan has no cache
		result, err := stateReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: ingress.Name}})
		require.NoError(t, err)
		require.False(t, result.Requeue)
	}()
	stateChan := stateReconciler.GetStateChan()
	state := <-stateChan
	domainConfig, ok := state[host]
	require.True(t, ok)
	require.Equal(t, cert, domainConfig.TlsCert.Cert)
	require.Equal(t, certKey, domainConfig.TlsCert.Key)
}
