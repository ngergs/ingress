package state

import (
	"github.com/rs/zerolog/log"
	v1Core "k8s.io/api/core/v1"
	v1Net "k8s.io/api/networking/v1"
	v1CoreListers "k8s.io/client-go/listers/core/v1"
)

// getTlsSecrets fetches for all secrets that are referenced in the ingressed the relevant kubernetes.io/tls secrets from the Kubernetes API
// and maps them to the hostname from the ingress spec.
func getTlsSecrets(secretLister v1CoreListers.SecretLister, ingresses []*v1Net.Ingress) TlsCerts {
	result := make(map[string]*TlsCert)
	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.TLS {
			secret, err := secretLister.Secrets(ingress.Namespace).Get(rule.SecretName)
			if err != nil {
				log.Warn().Err(err).Msgf("Error getting ingress TLS certificate secret %s in namespace %s, skipping entry.",
					rule.SecretName, ingress.Namespace)
				continue
			}
			if secret.Type != v1Core.SecretTypeTLS {
				log.Warn().Msgf("Secret type missmatch, required kubernetes.io/tls, but found %s for secret %s in namespace %s, skipping entry.",
					secret.Type, secret.Name, secret.Namespace)
				continue
			}
			for _, host := range rule.Hosts {
				result[host] = &TlsCert{
					Cert: secret.Data["tls.crt"],
					Key:  secret.Data["tls.key"],
				}
			}
		}
	}
	return result
}
