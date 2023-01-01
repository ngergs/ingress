package state

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	v1Core "k8s.io/api/core/v1"
	v1Net "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"net"
)

type IngressStateManager struct {
	k8sClients       *kubernetesClients
	ingressStateChan chan IngressState
	ingressClassName string
	hostIp           net.IP
}

type BackendPath struct {
	PathType    *v1Net.PathType
	Path        string
	Namespace   string
	ServiceName string
	ServicePort int32
}

type TlsCert struct {
	Cert []byte
	Key  []byte
}

type DomainConfig struct {
	BackendPaths []*BackendPath
	TlsCert      *TlsCert
}

type IngressState map[string]*DomainConfig

// getOrAddEmpty returns the map value for the given key. If the key does not exist in the map an empty entry is created and returned.
func (state IngressState) getOrAddEmpty(key string) *DomainConfig {
	val, ok := state[key]
	if ok {
		return val
	}
	val = &DomainConfig{
		BackendPaths: make([]*BackendPath, 0),
	}
	state[key] = val
	return val
}

// New creates a new Kubernetes Ingress state. The ctx can be used to cancel the listening to updates from the Kubernetes API.
// The hostIp is an optional argument. If and only if it is set the ingress status is updated.
func New(ctx context.Context, client kubernetes.Interface, ingressClassName string, hostIp net.IP) (*IngressStateManager, error) {
	k8sClients, err := newKubernetesClients(ctx, client)
	stateManager := &IngressStateManager{
		ingressClassName: ingressClassName,
		ingressStateChan: make(chan IngressState),
		hostIp:           hostIp,
		k8sClients:       k8sClients,
	}
	if err != nil {
		return nil, fmt.Errorf("failed to setup kubernetes clients")
	}

	go func() {
		for {
			select {
			case <-stateManager.k8sClients.addUpdDelChan:
				stateManager.refetchState()
			case <-ctx.Done():
				return
			}
		}
	}()

	return stateManager, nil
}

// GetStateChan returns a channel where state updates are delivered. This is the main method used to fetch the current status.
func (stateManager *IngressStateManager) GetStateChan() <-chan IngressState {
	return stateManager.ingressStateChan
}

// refetchState is used to collect a new state from the Kubernetes API from scratch.
func (stateManager *IngressStateManager) refetchState() {
	ingresses, err := stateManager.k8sClients.Ingress.Lister().List(labels.Everything())
	if err != nil {
		log.Error().Err(err).Msg("error listening ingresses")
		return
	}
	ingresses = filterByIngressClass(ingresses, stateManager.ingressClassName)

	ingressState := make(IngressState)
	stateManager.collectBackendPaths(ingresses, ingressState)
	stateManager.collectTlsSecrets(ingresses, ingressState)
	stateManager.ingressStateChan <- ingressState
}

// filterByIngressClass filters the ingresses and only selects those where the ingressClassName matches.
func filterByIngressClass(ingresses []*v1Net.Ingress, ingressClassName string) []*v1Net.Ingress {
	n := 0
	for _, el := range ingresses {
		if (el.Spec.IngressClassName != nil && *el.Spec.IngressClassName == ingressClassName) ||
			(el.Spec.IngressClassName == nil && el.Annotations["kubernetes.io/ingress.class"] == ingressClassName) {
			ingresses[n] = el
			n++
		}
	}
	return ingresses[:n]
}

// collectsBackendPaths collects the relevant backend path information and adds them to the ingress state. It also collects port numbers from referenced services.
func (stateManager *IngressStateManager) collectBackendPaths(ingresses []*v1Net.Ingress, result IngressState) {
	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			domainConfig := result.getOrAddEmpty(rule.Host)
			backendPaths := make([]*BackendPath, len(rule.HTTP.Paths))
			for i, path := range rule.HTTP.Paths {
				backendPath := &BackendPath{
					PathType:    path.PathType,
					Path:        path.Path,
					Namespace:   ingress.Namespace,
					ServiceName: path.Backend.Service.Name,
					ServicePort: path.Backend.Service.Port.Number,
				}
				err := stateManager.updatePortFromService(backendPath, path.Backend.Service.Port.Name)
				if err != nil {
					log.Warn().Err(err).Msgf("error getting service port %s referenced from backend path %s, skipping entry", path.Backend.Service.Port.Name, backendPath.Path)
					continue
				} else {
					backendPaths[i] = backendPath
				}
			}
			domainConfig.BackendPaths = append(domainConfig.BackendPaths, backendPaths...)
		}
	}
}

// updatePortFromService uses the Kubernetes API to fetch the Service status for the service referenced in the ingress config.
// If this has finished without error the config.ServicePort property is guaranteed to be set according to the current service spec.
func (stateManager *IngressStateManager) updatePortFromService(config *BackendPath, servicePortName string) error {
	if config.ServicePort == 0 && servicePortName == "" {
		return fmt.Errorf("invalid config for path %s. Backend service does contain neither port name nor port number", config.Path)
	}
	svc, err := stateManager.k8sClients.Service.Lister().Services(config.Namespace).Get(config.ServiceName)
	if err != nil {
		return err
	}

	// matching number takes precedence
	for _, svcPort := range svc.Spec.Ports {
		if svcPort.Port == config.ServicePort {
			return nil
		}
	}
	for _, svcPort := range svc.Spec.Ports {
		if svcPort.Name == servicePortName {
			config.ServicePort = svcPort.Port
			return nil
		}
	}
	return fmt.Errorf("port name %s specified but not found in service %s in namespace %s", servicePortName, config.ServiceName, config.Namespace)
}

// collectTlsSecrets fetches for all secrets that are referenced in the ingresses the relevant kubernetes.io/tls secrets from the Kubernetes API and adds them to the ingressState
func (stateManager *IngressStateManager) collectTlsSecrets(ingresses []*v1Net.Ingress, result IngressState) {
	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.TLS {
			secret, err := stateManager.k8sClients.Secret.Lister().Secrets(ingress.Namespace).Get(rule.SecretName)
			if err != nil {
				log.Warn().Err(err).Msgf("error getting ingress TLS certificate secret %s in namespace %s, skipping entry",
					rule.SecretName, ingress.Namespace)
				continue
			}
			if secret.Type != v1Core.SecretTypeTLS {
				log.Warn().Msgf("Secret type mismatch, required kubernetes.io/tls, but found %s for secret %s in namespace %s, skipping entry.",
					secret.Type, secret.Name, secret.Namespace)
				continue
			}
			for _, host := range rule.Hosts {
				domainConfig := result.getOrAddEmpty(host)
				domainConfig.TlsCert = &TlsCert{
					Cert: secret.Data["tls.crt"],
					Key:  secret.Data["tls.key"],
				}
			}
		}
	}
}
