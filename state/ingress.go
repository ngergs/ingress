package state

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	v1Core "k8s.io/api/core/v1"
	v1Net "k8s.io/api/networking/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	v1CoreListers "k8s.io/client-go/listers/core/v1"
	v1NetListers "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type IngressStateManager struct {
	ingressLister    v1NetListers.IngressLister
	serviceLister    v1CoreListers.ServiceLister
	secretLister     v1CoreListers.SecretLister
	ingressClassName string
	ingressStateChan chan *IngressState
	transport        *http.Transport
}

type BackendPaths map[string][]*IngressPathConfig // host->ingressPath
type TlsSecrets map[string]*v1Core.Secret         // host->secret

type IngressState struct {
	PathMap    BackendPaths
	TlsSecrets TlsSecrets
}

type IngressPathConfig struct {
	Namespace   string
	Config      *v1Net.HTTPIngressPath
	ServicePort *v1Core.ServicePort
}

// New creates a new Kubernetes Ingress state. The ctx can be used to cancel the listening.
func New(ctx context.Context, config *rest.Config, ingressClassName string) *IngressStateManager {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal().Err(err).Msg("error setting up k8s clients")
	}

	factoryGeneral := informers.NewSharedInformerFactory(client, 0)
	factorySecrets := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(
		func(list *v1.ListOptions) {
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
	informHandler := debounce(ctx, time.Duration(1)*time.Second, stateManager.refetchState)
	go stateManager.startInformer(ctx, ingressInformer.Informer(), informHandler)
	go stateManager.startInformer(ctx, serviceInformer.Informer(), informHandler)
	go stateManager.startInformer(ctx, secretInformer.Informer(), informHandler)
	return stateManager
}

func (stateManager *IngressStateManager) GetStateChan() <-chan *IngressState {
	return stateManager.ingressStateChan
}

func (stateManager *IngressStateManager) refetchState() {
	ingresses, err := stateManager.ingressLister.List(labels.Everything())
	if err != nil {
		log.Error().Err(err).Msg("error listening ingresses")
		return
	}
	ingresses = filterByIngressClass(ingresses, stateManager.ingressClassName)
	ingressState := &IngressState{
		PathMap:    getBackendPaths(stateManager.serviceLister, ingresses),
		TlsSecrets: getTlsSecrets(stateManager.secretLister, ingresses),
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
