package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/jarcoal/httpmock"
	"github.com/madflojo/testcerts"
	"github.com/ngergs/ingress/revproxy"
	"github.com/ngergs/ingress/state"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"io"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"net/http"
	"os"
	"testing"
	"time"
)

const (
	testTimeout     = 60 * time.Second
	testContainer   = "docker.io/rancher/k3s:v1.28.2-k3s1"
	svcName         = "app"
	svcPort         = 8081
	namespace       = "test"
	responseContent = "Hello World!"
	host            = "localhost"
	httpsTestPort   = 8443
)

func TestIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()
	svcUrl := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/", svcName, namespace, svcPort)
	httpmock.RegisterResponder("GET", svcUrl, httpmock.NewStringResponder(200, responseContent))
	revProxy, ingressStateReconciler, c, shutdown := setupCluster(ctx, t)
	defer shutdown()
	ca := setupK8sApp(ctx, t, c)
	go func() {
		err := ingressStateReconciler.Start(ctx)
		require.NoError(t, err)
	}()
	tlsServer := getServer(nil, revProxy.GetHandlerProxying())
	tlsConfig := getTlsConfig(revProxy.GetCertificateFunc())
	go func() {
		err := listenAndServeTls(httpsTestPort, tlsServer, tlsConfig)
		require.NoError(t, err)
	}()
	time.Sleep(time.Millisecond)

	// not affected by httpmock as we are not using the default transport
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: ca.CertPool(),
			}}}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	var r *http.Response
LOOP:
	for {
		select {
		case <-ctx.Done():
			t.Error("error waiting for non HTTP 503 response from the reverse proxy")
		case <-ticker.C:
			req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://%s:%d", host, httpsTestPort), nil)
			require.NoError(t, err)
			r, err = httpClient.Do(req)
			if err == nil {
				break LOOP
			}
		}
	}
	require.Equal(t, 200, r.StatusCode)
	defer func() {
		err := r.Body.Close()
		require.NoError(t, err)
	}()
	data, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	require.Equal(t, responseContent, string(data))
	require.Equal(t, 1, httpmock.GetTotalCallCount())
	require.Equal(t, 1, httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", svcUrl)])
}

func setupK8sApp(ctx context.Context, t *testing.T, c *kubernetes.Clientset) *testcerts.CertificateAuthority {
	_ = setupNamespace(ctx, t, c)
	svc := setupService(ctx, t, c)
	certSecret, ca := setupCertSecret(ctx, t, c)
	_ = setupIngress(ctx, t, c, svc, certSecret)
	return ca
}

func setupIngress(ctx context.Context, t *testing.T, c *kubernetes.Clientset, svc *corev1.Service, certSecret *corev1.Secret) *netv1.Ingress {
	require.Equal(t, len(svc.Spec.Ports), 1)
	pathTypePrefix := netv1.PathTypePrefix
	ingress, err := c.NetworkingV1().Ingresses(namespace).Create(ctx, &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: namespace,
		},
		Spec: netv1.IngressSpec{
			IngressClassName: ingressClassName,
			Rules: []netv1.IngressRule{{
				Host: host,
				IngressRuleValue: netv1.IngressRuleValue{HTTP: &netv1.HTTPIngressRuleValue{
					Paths: []netv1.HTTPIngressPath{{
						PathType: &pathTypePrefix,
						Path:     "/",
						Backend: netv1.IngressBackend{
							Service: &netv1.IngressServiceBackend{
								Name: svc.Name,
								Port: netv1.ServiceBackendPort{Name: svc.Spec.Ports[0].Name},
							},
						},
					}},
				},
				},
			}},
			TLS: []netv1.IngressTLS{{
				Hosts:      []string{host},
				SecretName: certSecret.Name,
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	return ingress
}

func setupCertSecret(ctx context.Context, t *testing.T, c *kubernetes.Clientset) (*corev1.Secret, *testcerts.CertificateAuthority) {
	ca := testcerts.NewCA()
	// Create a signed Certificate and Key for "localhost"
	certs, err := ca.NewKeyPair(host)
	require.NoError(t, err)
	secret, err := c.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-cert", host),
			Namespace: namespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": certs.PublicKey(),
			"tls.key": certs.PrivateKey(),
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	return secret, ca
}

func setupService(ctx context.Context, t *testing.T, c *kubernetes.Clientset) *corev1.Service {
	svc, err := c.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app.kubernetes.io/name": "app"},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				TargetPort: intstr.IntOrString{Type: intstr.Int, IntVal: 8080},
				Port:       svcPort,
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	return svc
}

func setupNamespace(ctx context.Context, t *testing.T, c *kubernetes.Clientset) *corev1.Namespace {
	namespace, err := c.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	return namespace
}

// internal function to setup a kubernetes testcontainer
func setupCluster(ctx context.Context, t *testing.T) (reverseProxy *revproxy.ReverseProxy, ingressStateReconciler *state.IngressReconciler,
	clientSet *kubernetes.Clientset, shutdown func()) {
	k3sContainer, err := k3s.RunContainer(ctx,
		testcontainers.WithImage(testContainer),
	)
	require.NoError(t, err)
	shutdown = func() {
		err := k3sContainer.Terminate(ctx)
		require.NoError(t, err)
	}
	kubeConfigYaml, err := k3sContainer.GetKubeConfig(ctx)
	require.NoError(t, err)
	err = os.WriteFile("/home/niklas/.kube/config", kubeConfigYaml, 0644)
	require.NoError(t, err)
	k8sConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYaml)
	require.NoError(t, err)
	clientSet, err = kubernetes.NewForConfig(k8sConfig)
	require.NoError(t, err)

	mgr, err := setupControllerManager(k8sConfig)
	require.NoError(t, err)
	reverseProxy, ingressStateReconciler, err = setupReverseProxy(ctx, mgr)
	require.NoError(t, err)
	return
}
