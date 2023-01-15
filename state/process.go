package state

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	v1Core "k8s.io/api/core/v1"
	v1Net "k8s.io/api/networking/v1"
	"net"
	"strings"
	"sync"
)

// BackendPath is a data struct that holds the properties of a certain service backend
type BackendPath struct {
	PathType    *v1Net.PathType
	Path        string
	Namespace   string
	ServiceName string
	ServicePort int32
}

// TlsCert is a data struct that holds a tls certificate and private kay
type TlsCert struct {
	Cert []byte
	Key  []byte
}

// DomainConfig is the Ingress state for a specific domain
type DomainConfig struct {
	BackendPaths []*BackendPath
	TlsCert      *TlsCert
}

// IngressState is the current state of the ingress configurations
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

type ingressStatusUpdate struct {
	Ingress *v1Net.Ingress
	Status  *v1Net.IngressLoadBalancerIngress
}

// processState processed the current input State and returns the processed state as well as
// needed updates for the ingress status
func (r *IngressReconciler) processState() (state IngressState, updates []*ingressStatusUpdate) {
	state = make(IngressState)
	updates = make([]*ingressStatusUpdate, 0)
	for _, ingress := range r.ingressState {
		errors := r.collectBackendPaths(ingress, state)
		errors = append(errors, r.collectTlsSecrets(ingress, state)...)
		log.Debug().Msgf("ingress errors: %v", errors)
		if r.hostIp != nil {
			updates = append(updates, &ingressStatusUpdate{
				Ingress: ingress.DeepCopy(),
				Status:  statusFromErrors(errors, r.hostIp),
			})
		}
	}
	return state, updates
}

// updateStatus updates the k8s ingress status, blocks till finished.
func (r *IngressReconciler) updateStatus(ctx context.Context, updates []*ingressStatusUpdate) []error {
	errors := make([]error, 0)
	var errorMu sync.Mutex
	if r.hostIp != nil {
		var wg sync.WaitGroup
		wg.Add(len(updates))
		for _, el := range updates {
			go func(update *ingressStatusUpdate) {
				err := r.k8sClients.updateIngressStatus(ctx, update.Ingress, update.Status)
				if err != nil {
					errorMu.Lock()
					errors = append(errors, fmt.Errorf("failed to update ingress status: %v", err))
					errorMu.Unlock()
				}
				wg.Done()
			}(el)
		}
		// make this blocking to make sure that we not start a new fetch before the status is updated
		wg.Wait()
	}
	return errors
}

// statusFromErrors builds an ingress status from the given error list
func statusFromErrors(errors []error, hostIp net.IP) *v1Net.IngressLoadBalancerIngress {
	var errMsg *string
	if len(errors) > 0 {
		var sb strings.Builder
		for i, err := range errors {
			sb.WriteString(err.Error())
			if i < len(errors)-1 {
				sb.WriteString(";")
			}
		}
		errMsgCollected := sb.String()
		errMsg = &errMsgCollected
	}
	return &v1Net.IngressLoadBalancerIngress{
		IP: hostIp.String(),
		Ports: []v1Net.IngressPortStatus{
			{Port: 80,
				Protocol: "TCP",
				Error:    errMsg,
			},
			{Port: 443,
				Protocol: "TCP",
				Error:    errMsg,
			},
		},
	}
}

// collectsBackendPaths collects the relevant backend path information and adds them to the ingress state. It also collects port numbers from referenced services.
func (r *IngressReconciler) collectBackendPaths(ingress *v1Net.Ingress, result IngressState) []error {
	errors := make([]error, 0)
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		domainConfig := result.getOrAddEmpty(rule.Host)
		backendPaths := make([]*BackendPath, 0)
		for _, path := range rule.HTTP.Paths {
			backendPath := &BackendPath{
				PathType:    path.PathType,
				Path:        path.Path,
				Namespace:   ingress.Namespace,
				ServiceName: path.Backend.Service.Name,
				ServicePort: path.Backend.Service.Port.Number,
			}
			err := r.updatePortFromService(backendPath, path.Backend.Service.Port.Name)
			if err != nil {
				log.Warn().Err(err).Msgf("could not determine service port: %s for backend service %s in namespace %s", path.Backend.Service.Port.Name, path.Backend.Service.Name, ingress.Namespace)
				errors = append(errors, fmt.Errorf("ngergs.de/ServicePortNotFound: %s for backend service %s", path.Backend.Service.Port.Name, path.Backend.Service.Name))
				continue
			} else {
				backendPaths = append(backendPaths, backendPath)
			}
		}
		domainConfig.BackendPaths = append(domainConfig.BackendPaths, backendPaths...)
	}
	return errors
}

// updatePortFromService uses the Kubernetes API to fetch the ServiceInformer status for the service referenced in the ingress config.
// If this has finished without error the config.ServicePort property is guaranteed to be set according to the current service spec.
func (r *IngressReconciler) updatePortFromService(config *BackendPath, servicePortName string) error {
	if config.ServicePort == 0 && servicePortName == "" {
		return fmt.Errorf("invalid config for path %s. Backend service does contain neither port name nor port number", config.Path)
	}
	svc, err := r.k8sClients.ServiceLister.Services(config.Namespace).Get(config.ServiceName)
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
func (r *IngressReconciler) collectTlsSecrets(ingress *v1Net.Ingress, result IngressState) []error {
	errors := make([]error, 0)
	for _, rule := range ingress.Spec.TLS {
		secret, err := r.k8sClients.SecretLister.Secrets(ingress.Namespace).Get(rule.SecretName)
		if err != nil {
			log.Warn().Err(err).Msgf("error getting ingress TLS certificate secret %s in namespace %s",
				rule.SecretName, ingress.Namespace)
			errors = append(errors, fmt.Errorf("ngergs.de/TlsCertMissing: referenced secret %s", rule.SecretName))
			continue
		}
		if secret.Type != v1Core.SecretTypeTLS {
			log.Warn().Msgf("SecretInformer type mismatch, required kubernetes.io/tls, but found %s for secret %s in namespace %s",
				secret.Type, secret.Name, secret.Namespace)
			errors = append(errors, fmt.Errorf("ngergs.de/TlsCertWrongType: has to be kubernetees.io/tls"))
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
	return errors
}
