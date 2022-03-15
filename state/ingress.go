package state

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	v1Net "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	v1CoreListers "k8s.io/client-go/listers/core/v1"
	v1NetListers "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"
)

type IngressStateManager struct {
	ingressLister    v1NetListers.IngressLister
	serviceLister    v1CoreListers.ServiceLister
	secretLister     v1CoreListers.SecretLister
	ingressClassName string
	ingressStateChan chan *IngressState
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
func New(ctx context.Context, client kubernetes.Interface, ingressClassName string, options ...ConfigOption) *IngressStateManager {
	config := defaultConfig.clone().applyOptions(options...)

	factoryGeneral := informers.NewSharedInformerFactory(client, 0)
	factorySecrets := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(
		func(list *v1Meta.ListOptions) {
			list.FieldSelector = fields.OneTermEqualSelector("type", "kubernetes.io/tls").String()
		}))
	ingressInformer := factoryGeneral.Networking().V1().Ingresses()
	serviceInformer := factoryGeneral.Core().V1().Services()
	secretInformer := factorySecrets.Core().V1().Secrets()

	stateManager := &IngressStateManager{
		ingressLister:    ingressInformer.Lister(),
		serviceLister:    serviceInformer.Lister(),
		secretLister:     secretInformer.Lister(),
		ingressClassName: ingressClassName,
		ingressStateChan: make(chan *IngressState),
	}

	// Start listening to relevant API objects for changes
	informHandler := stateManager.refetchState
	if config.DebounceDuration != time.Duration(0) {
		informHandler = debounce(ctx, config.DebounceDuration, informHandler)
	}
	go stateManager.startInformer(ctx, ingressInformer.Informer(), informHandler)
	go stateManager.startInformer(ctx, serviceInformer.Informer(), informHandler)
	go stateManager.startInformer(ctx, secretInformer.Informer(), informHandler)
	return stateManager
}

// GetStateChan returns a channel where state updates are delivered. This is the main method used to fetch the current status.
func (stateManager *IngressStateManager) GetStateChan() <-chan *IngressState {
	return stateManager.ingressStateChan
}

// refetchState is used to collect a new state from the Kubernetes API from scratch.
func (stateManager *IngressStateManager) refetchState() {
	ingresses, err := stateManager.ingressLister.List(labels.Everything())
	if err != nil {
		log.Error().Err(err).Msg("error listening ingresses")
		return
	}
	ingresses = filterByIngressClass(ingresses, stateManager.ingressClassName)
	ingressState := &IngressState{
		BackendPaths: getBackendPaths(stateManager.serviceLister, ingresses),
		TlsCerts:     getTlsSecrets(stateManager.secretLister, ingresses),
	}
	stateManager.ingressStateChan <- ingressState
}

func (stateManager *IngressStateManager) startInformer(ctx context.Context, informer cache.SharedIndexInformer, handler func()) {
	wrappedHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			log.Debug().Msgf("Received k8s add update %v", obj)
			handler()
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			log.Debug().Msgf("Received k8s update, new: %v, old: %v", oldObj, newObj)
			handler()
		},
		DeleteFunc: func(obj interface{}) {
			log.Debug().Msgf("Received k8s delete update %v", obj)
			handler()
		},
	}
	informer.AddEventHandler(wrappedHandler)
	informer.Run(ctx.Done())
}
