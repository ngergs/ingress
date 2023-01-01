package state

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	v1Core "k8s.io/api/core/v1"
	v1Net "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	v1ClientCore "k8s.io/client-go/informers/core/v1"
	v1ClientNet "k8s.io/client-go/informers/networking/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"net"
	"sync"
	"sync/atomic"
)

type IngressStateManager struct {
	factories        []informers.SharedInformerFactory
	ingressInformer  v1ClientNet.IngressInformer
	serviceInformer  v1ClientCore.ServiceInformer
	secretInformer   v1ClientCore.SecretInformer
	ingressStateChan chan IngressState
	ingressClassName string
	hostIp           net.IP
	refetchMu        sync.Mutex
	refetchQueued    atomic.Bool
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
	factoryGeneral := informers.NewSharedInformerFactory(client, 0)
	factorySecrets := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(
		func(list *v1Meta.ListOptions) {
			list.FieldSelector = fields.OneTermEqualSelector("type", "kubernetes.io/tls").String()
		}))
	ingressInformer := factoryGeneral.Networking().V1().Ingresses()
	serviceInformer := factoryGeneral.Core().V1().Services()
	secretInformer := factorySecrets.Core().V1().Secrets()

	stateManager := &IngressStateManager{
		factories:        []informers.SharedInformerFactory{factoryGeneral, factorySecrets},
		ingressInformer:  ingressInformer,
		serviceInformer:  serviceInformer,
		secretInformer:   secretInformer,
		ingressClassName: ingressClassName,
		ingressStateChan: make(chan IngressState),
		hostIp:           hostIp,
	}

	if err := stateManager.startInformers(ctx); err != nil {
		return nil, err
	}
	return stateManager, nil
}

// GetStateChan returns a channel where state updates are delivered. This is the main method used to fetch the current status.
func (stateManager *IngressStateManager) GetStateChan() <-chan IngressState {
	return stateManager.ingressStateChan
}

// refetchState is used to collect a new state from the Kubernetes API from scratch.
// multiple calls will only result in a single refetch invocation.
// The logic is to wait till the k8s informers are synced, any parallel calls prior to this point
// only result in a single refetch. Once the k8s informers are synced a call to this function queues
// a new refetch.
func (stateManager *IngressStateManager) refetchState(ctx context.Context) {
	log.Debug().Msg("refetchState called")
	if !stateManager.refetchQueued.CompareAndSwap(false, true) {
		log.Debug().Msg("refetch already queued")
		return
	}
	log.Debug().Msg("refetchState waits for k8s informers to sync")
	stateManager.waitForSync(ctx)
	stateManager.refetchMu.Lock()
	defer stateManager.refetchMu.Unlock()
	stateManager.refetchQueued.Store(false)
	log.Debug().Msg("refetchState determines new state")
	ingresses, err := stateManager.ingressInformer.Lister().List(labels.Everything())
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
	svc, err := stateManager.serviceInformer.Lister().Services(config.Namespace).Get(config.ServiceName)
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
			secret, err := stateManager.secretInformer.Lister().Secrets(ingress.Namespace).Get(rule.SecretName)
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

// setupInformers setups and start all internal informers and sets the refetchState function as handler for AddFunc, UpdateFunc, DeleteFunc.
func (stateManager *IngressStateManager) startInformers(ctx context.Context) error {
	if err := stateManager.setupInformer(ctx, stateManager.ingressInformer.Informer(), true); err != nil {
		return fmt.Errorf("failed to setup ingress informer: %v", err)
	}
	if err := stateManager.setupInformer(ctx, stateManager.serviceInformer.Informer(), true); err != nil {
		return fmt.Errorf("failed to setup services informer: %v", err)
	}
	if err := stateManager.setupInformer(ctx, stateManager.secretInformer.Informer(), false); err != nil {
		return fmt.Errorf("failed to setup secret informer: %v", err)
	}

	for _, factory := range stateManager.factories {
		factory.Start(ctx.Done())
	}
	return nil
}

// setupInformer setups the given informer and sets the refetchState function as handler for AddFunc, UpdateFunc, DeleteFunc.
func (stateManager *IngressStateManager) setupInformer(ctx context.Context, informer cache.SharedIndexInformer, logDebug bool) error {
	wrappedHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if logDebug {
				log.Debug().Msgf("Received k8s add update %v", obj)
			}
			go stateManager.refetchState(ctx)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if logDebug {
				log.Debug().Msgf("Received k8s update, new: %v, old: %v", oldObj, newObj)
			}
			go stateManager.refetchState(ctx)
		},
		DeleteFunc: func(obj interface{}) {
			if logDebug {
				log.Debug().Msgf("Received k8s delete update %v", obj)
			}
			go stateManager.refetchState(ctx)
		},
	}
	_, err := informer.AddEventHandler(wrappedHandler)
	return err
}

// waitFroSync waits till all factories sync. No specific order is enforced.
func (stateManager *IngressStateManager) waitForSync(ctx context.Context) {
	if len(stateManager.factories) == 0 {
		return
	}
	var wg sync.WaitGroup
	wg.Add(len(stateManager.factories))
	for i, el := range stateManager.factories {
		go func(index int, factory informers.SharedInformerFactory) {
			log.Debug().Msgf("Waiting for informer from factory %d", index)
			factory.WaitForCacheSync(ctx.Done())
			log.Debug().Msgf("Waited for informer from factory %d", index)
			wg.Done()
		}(i, el)
	}
	wg.Wait()
}
