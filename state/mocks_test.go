package state

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	v1Net "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const host = "localhost"
const namespace = "default"
const path = "/test"
const serviceName = "svc"
const servicePort int32 = 8080
const servicePortName = "port"
const secretName = "secret"

// getDummyIngress returns a dummy Kubernetes IngressInformer API-Ressource. Neither ServiceInformer port nor port name are set and have to be set for tests.
func getDummyIngress() *v1Net.Ingress {
	return &v1Net.Ingress{
		ObjectMeta: v1Meta.ObjectMeta{
			Name: "ingress",
		},
		Spec: v1Net.IngressSpec{
			IngressClassName: &ingressClassName,
			Rules: []v1Net.IngressRule{{
				Host: host,
				IngressRuleValue: v1Net.IngressRuleValue{
					HTTP: &v1Net.HTTPIngressRuleValue{
						Paths: []v1Net.HTTPIngressPath{{
							Path:     path,
							PathType: &pathType,
							Backend: v1Net.IngressBackend{
								Service: &v1Net.IngressServiceBackend{
									Name: serviceName,
									Port: v1Net.ServiceBackendPort{Name: servicePortName}}}}}}}}}}}
}

// getDummyIngress returns a dummy Kubernetes IngressInformer API-Ressource. Neither ServiceInformer port nor port name are set and have to be set for tests.
func getDummyIngressSecretRef() *v1Net.Ingress {
	return &v1Net.Ingress{
		ObjectMeta: v1Meta.ObjectMeta{
			Name: "ingress",
		},
		Spec: v1Net.IngressSpec{
			IngressClassName: &ingressClassName,
			TLS: []v1Net.IngressTLS{{
				Hosts:      []string{host},
				SecretName: secretName,
			}}}}
}

// getDummyService returns a dummy Kubernetes ServiceInformer API-Ressource.
func getDummyService() *v1.Service {
	return &v1.Service{
		ObjectMeta: v1Meta.ObjectMeta{
			Name: serviceName,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{
				Name: servicePortName,
				Port: servicePort,
			}}}}
}

// getDummyService returns a dummy Kubernetes ServiceInformer API-Ressource.
func getDummySecret(t *testing.T) (secret *v1.Secret, cert []byte, certKey []byte) {
	var secretDataCert [20]byte
	var secretDataCertKey [20]byte
	_, err := rand.Read(secretDataCert[:])
	assert.Nil(t, err)
	_, err = rand.Read(secretDataCertKey[:])
	assert.Nil(t, err)
	return &v1.Secret{
		ObjectMeta: v1Meta.ObjectMeta{
			Name: secretName,
		},
		Type: v1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": secretDataCert[:],
			"tls.key": secretDataCertKey[:],
		},
	}, secretDataCert[:], secretDataCertKey[:]
}
