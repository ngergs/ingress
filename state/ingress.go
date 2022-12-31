package state

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	v1Net "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	v1ClientCore "k8s.io/client-go/informers/core/v1"
	v1ClientNet "k8s.io/client-go/informers/networking/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"sync"
	"sync/atomic"
)

type IngressStateManager struct {
	factories        []informers.SharedInformerFactory
	ingressInformer  v1ClientNet.IngressInformer
	serviceInformer  v1ClientCore.ServiceInformer
	secretInformer   v1ClientCore.SecretInformer
	ingressStateChan chan *IngressState
	ingressClassName string
	refetchMu        sync.Mutex
	refetchQueued    atomic.Bool
}

type BackendPaths map[string][]*PathConfig // host->ingressPath
type TlsCerts map[string]*TlsCert          // host->secret

type PathConfig struct {
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

type IngressState struct {
	BackendPaths BackendPaths
	TlsCerts     TlsCerts
}

// New creates a new Kubernetes Ingress state. The ctx can be used to cancel the listening to updates from the Kubernetes API.
func New(ctx context.Context, client kubernetes.Interface, ingressClassName string) (*IngressStateManager, error) {
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
		ingressStateChan: make(chan *IngressState),
	}

	if err := stateManager.startInformers(ctx); err != nil {
		return nil, err
	}
	return stateManager, nil
}

// GetStateChan returns a channel where state updates are delivered. This is the main method used to fetch the current status.
func (stateManager *IngressStateManager) GetStateChan() <-chan *IngressState {
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
	ingressState := &IngressState{
		BackendPaths: getBackendPaths(stateManager.serviceInformer.Lister(), ingresses),
		TlsCerts:     getTlsSecrets(stateManager.secretInformer.Lister(), ingresses),
	}
	stateManager.ingressStateChan <- ingressState
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
	informer.AddEventHandler(wrappedHandler)
	return nil
}

// waitFroSync waits till all factories sync. No specific order is enforced.
func (stateManager *IngressStateManager) waitForSync(ctx context.Context) {
	if len(stateManager.factories) == 0 {
		return
	}
	var wg sync.WaitGroup
	wg.Add(len(stateManager.factories))
	for i, el := range stateManager.factories {
		go func(factory informers.SharedInformerFactory) {
			log.Debug().Msgf("Waiting for informer from factory %d", i)
			factory.WaitForCacheSync(ctx.Done())
			log.Debug().Msgf("Waited for informer from factory %d", i)
			wg.Done()
		}(el)
	}
	wg.Wait()
}
