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
	"strings"
	"sync"
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

// New creates a new Kubernetes IngressInformer state. The ctx can be used to cancel the listening to updates from the Kubernetes API.
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
				stateManager.refetchState(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
	return stateManager, k8sClients.startInformers(ctx)
}

// GetStateChan returns a channel where state updates are delivered. This is the main method used to fetch the current status.
func (stateManager *IngressStateManager) GetStateChan() <-chan IngressState {
	return stateManager.ingressStateChan
}

// getIngresses fetches the list of ingresses with the relevant ingress class from the kubernetes api
func (stateManager *IngressStateManager) getIngresses() ([]*v1Net.Ingress, error) {
	ingresses, err := stateManager.k8sClients.IngressInformer.Lister().List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("error fetching ingress list: %v", err)
	}
	//filter the ingress class
	n := 0
	for _, el := range ingresses {
		if (el.Spec.IngressClassName != nil && *el.Spec.IngressClassName == stateManager.ingressClassName) ||
			(el.Spec.IngressClassName == nil && el.Annotations["kubernetes.io/ingress.class"] == stateManager.ingressClassName) {
			ingresses[n] = el
			n++
		}
	}
	return ingresses[:n], nil
}

// CleanIngressStatus is supposed to be called during shutdown and removes all ingress status entries set by this instance.
// The internal state channel is not updated.
func (stateManager *IngressStateManager) CleanIngressStatus(ctx context.Context) []error {
	ingresses, err := stateManager.getIngresses()
	if err != nil {
		return []error{err}
	}
	errors := make([]error, 0)
	errChan := make(chan error)
	defer close(errChan) // to stop the error collection goroutine
	go func() {
		for err := range errChan {
			errors = append(errors, err)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(len(ingresses))
	for _, el := range ingresses {
		go func(ingress *v1Net.Ingress) {
			err := stateManager.k8sClients.cleanIngressStatus(ctx, ingress, stateManager.hostIp)
			if err != nil {
				errChan <- fmt.Errorf("could not clean ingress status: %v", err)
			}
			wg.Done()
		}(el)
	}
	wg.Wait()

	return errors
}

// refetchState is used to collect a new state from the Kubernetes API from scratch.
func (stateManager *IngressStateManager) refetchState(ctx context.Context) {
	ingresses, err := stateManager.getIngresses()
	if err != nil {
		log.Error().Err(err).Msg("error listening ingresses")
		return
	}

	ingressState := make(IngressState)
	updates := make([]*ingressStatusUpdate, 0)
	for _, ingress := range ingresses {
		errors := stateManager.collectBackendPaths(ingress, ingressState)
		errors = append(errors, stateManager.collectTlsSecrets(ingress, ingressState)...)
		log.Debug().Msgf("ingress errors: %v", errors)
		if stateManager.hostIp != nil {
			updates = append(updates, &ingressStatusUpdate{
				Ingress: ingress.DeepCopy(),
				Status:  statusFromErrors(errors, stateManager.hostIp),
			})
		}
	}
	stateManager.ingressStateChan <- ingressState
	if stateManager.hostIp != nil {
		var wg sync.WaitGroup
		wg.Add(len(updates))
		for _, el := range updates {
			go func(update *ingressStatusUpdate) {
				err := stateManager.k8sClients.updateIngressStatus(ctx, update.Ingress, update.Status)
				if err != nil {
					log.Error().Err(err).Msg("failed to update ingress status")
				}
				wg.Done()
			}(el)
		}
		// make this blocking to make sure that we not start a new fetch before the status is updated
		wg.Wait()
	}
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
func (stateManager *IngressStateManager) collectBackendPaths(ingress *v1Net.Ingress, result IngressState) []error {
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
			err := stateManager.updatePortFromService(backendPath, path.Backend.Service.Port.Name)
			if err != nil {
				log.Warn().Err(err).Msg("could not determine service port")
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
func (stateManager *IngressStateManager) updatePortFromService(config *BackendPath, servicePortName string) error {
	if config.ServicePort == 0 && servicePortName == "" {
		return fmt.Errorf("invalid config for path %s. Backend service does contain neither port name nor port number", config.Path)
	}
	svc, err := stateManager.k8sClients.ServiceInformer.Lister().Services(config.Namespace).Get(config.ServiceName)
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
func (stateManager *IngressStateManager) collectTlsSecrets(ingress *v1Net.Ingress, result IngressState) []error {
	errors := make([]error, 0)
	for _, rule := range ingress.Spec.TLS {
		secret, err := stateManager.k8sClients.SecretInformer.Lister().Secrets(ingress.Namespace).Get(rule.SecretName)
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
