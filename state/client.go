package state

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	v1ClientCore "k8s.io/client-go/informers/core/v1"
	v1ClientNet "k8s.io/client-go/informers/networking/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"sync"
	"sync/atomic"
)

// kubernetesClients provides informers and ingress kubernetes clients for ingress updates.
type kubernetesClients struct {
	Ingress                 v1ClientNet.IngressInformer
	Service                 v1ClientCore.ServiceInformer
	Secret                  v1ClientCore.SecretInformer
	factories               []informers.SharedInformerFactory
	addUpdDelChan           chan struct{}
	addUpdDelCallbackMu     sync.Mutex
	addUpdDelCallbackQueued atomic.Bool
}

// AddUpdDelChan returns a signal channel that is triggered on add, update or delete calls from Kubernetes.
// The channel is triggered after syncing of the internal informers has been restored.
// Multiple calls during the resync period are automatically debounced to one callback call.
func (c *kubernetesClients) AddUpdDelChan() <-chan struct{} {
	return c.addUpdDelChan
}

// newKubernetesClients creates a new kubernetesClients struct. The ctx can be used to cancel the listening to updates from the Kubernetes API.
func newKubernetesClients(ctx context.Context, client kubernetes.Interface) (*kubernetesClients, error) {
	factoryGeneral := informers.NewSharedInformerFactory(client, 0)
	factorySecrets := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(
		func(list *v1Meta.ListOptions) {
			list.FieldSelector = fields.OneTermEqualSelector("type", "kubernetes.io/tls").String()
		}))
	clients := &kubernetesClients{
		factories:     []informers.SharedInformerFactory{factoryGeneral, factorySecrets},
		Ingress:       factoryGeneral.Networking().V1().Ingresses(),
		Service:       factoryGeneral.Core().V1().Services(),
		Secret:        factorySecrets.Core().V1().Secrets(),
		addUpdDelChan: make(chan struct{}),
	}
	return clients, clients.startInformers(ctx)
}

// setupInformers setups and start all internal informers and sets the refetchState function as handler for AddFunc, UpdateFunc, DeleteFunc.
func (c *kubernetesClients) startInformers(ctx context.Context) error {
	if err := c.setupInformer(ctx, c.Ingress.Informer(), true); err != nil {
		return fmt.Errorf("failed to setup ingress informer: %v", err)
	}
	if err := c.setupInformer(ctx, c.Service.Informer(), true); err != nil {
		return fmt.Errorf("failed to setup services informer: %v", err)
	}
	if err := c.setupInformer(ctx, c.Secret.Informer(), false); err != nil {
		return fmt.Errorf("failed to setup secret informer: %v", err)
	}

	for _, factory := range c.factories {
		factory.Start(ctx.Done())
	}
	return nil
}

// setupInformer setups the given informer and sets the refetchState function as handler for AddFunc, UpdateFunc, DeleteFunc.
func (c *kubernetesClients) setupInformer(ctx context.Context, informer cache.SharedIndexInformer, logDebug bool) error {
	wrappedHandler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if logDebug {
				log.Debug().Msgf("Received k8s add update %v", obj)
			}
			go c.signalUpdateAfterSync(ctx)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if logDebug {
				log.Debug().Msgf("Received k8s update, new: %v, old: %v", oldObj, newObj)
			}
			go c.signalUpdateAfterSync(ctx)
		},
		DeleteFunc: func(obj interface{}) {
			if logDebug {
				log.Debug().Msgf("Received k8s delete update %v", obj)
			}
			go c.signalUpdateAfterSync(ctx)
		},
	}
	_, err := informer.AddEventHandler(wrappedHandler)
	return err
}

// signalUpdateAfterSync calls the callback after syncing of the internal informers has been restored.
// Multiple calls during the resync period are automatically debounced to one callback call.
func (c *kubernetesClients) signalUpdateAfterSync(ctx context.Context) {
	log.Debug().Msg("k8s update callback called")
	if !c.addUpdDelCallbackQueued.CompareAndSwap(false, true) {
		log.Debug().Msg("k8s update callback already queued")
		return
	}
	log.Debug().Msg("k8s callback wrapper waits for k8s informers to sync")
	c.waitForSync(ctx)
	c.addUpdDelCallbackMu.Lock()
	defer c.addUpdDelCallbackMu.Unlock()
	c.addUpdDelCallbackQueued.Store(false)
	log.Debug().Msg("calling k8s update callback func")
	c.addUpdDelChan <- struct{}{}
}

// waitFroSync waits till all factories sync. No specific order is enforced.
func (c *kubernetesClients) waitForSync(ctx context.Context) {
	if len(c.factories) == 0 {
		return
	}
	var wg sync.WaitGroup
	wg.Add(len(c.factories))
	for i, el := range c.factories {
		go func(index int, factory informers.SharedInformerFactory) {
			log.Debug().Msgf("Waiting for informer from factory %d", index)
			factory.WaitForCacheSync(ctx.Done())
			log.Debug().Msgf("Waited for informer from factory %d", index)
			wg.Done()
		}(i, el)
	}
	wg.Wait()
}
