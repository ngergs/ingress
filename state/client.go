package state

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	v1ClientCore "k8s.io/client-go/informers/core/v1"
	v1ClientNet "k8s.io/client-go/informers/networking/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"net"
	"sync"
	"sync/atomic"
)

// kubernetesClients provides informers and ingress kubernetes clients for ingress updates.
type kubernetesClients struct {
	IngressInformer         v1ClientNet.IngressInformer
	ServiceInformer         v1ClientCore.ServiceInformer
	SecretInformer          v1ClientCore.SecretInformer
	factories               []informers.SharedInformerFactory
	addUpdDelChan           chan struct{}
	addUpdDelCallbackMu     sync.Mutex
	addUpdDelCallbackQueued atomic.Bool
	client                  kubernetes.Interface
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
		factories:       []informers.SharedInformerFactory{factoryGeneral, factorySecrets},
		IngressInformer: factoryGeneral.Networking().V1().Ingresses(),
		ServiceInformer: factoryGeneral.Core().V1().Services(),
		SecretInformer:  factorySecrets.Core().V1().Secrets(),
		addUpdDelChan:   make(chan struct{}),
		client:          client,
	}
	return clients, clients.startInformers(ctx)
}

// updateIngressStatus updates the ingress status and syncs the result with Kubernetes (if changes have occurred)
func (c *kubernetesClients) updateIngressStatus(ctx context.Context, ingress *v1.Ingress, status *v1.IngressLoadBalancerIngress) error {
	currentStatus, _, ok := findIngressStatus(ingress.Status.LoadBalancer.Ingress, status.IP)
	// we set the message for both ports equal so no need to differentiate here
	if ok && statusEqual(currentStatus, status) {
		return nil
	}
	return c.syncIngressStatus(ctx, ingress, func(ingressStatus []v1.IngressLoadBalancerIngress) ([]v1.IngressLoadBalancerIngress, bool) {
		log.Debug().Msgf("Setting/Updating ingress status for %s in namespace %s", ingress.Name, ingress.Namespace)
		if ok && statusContained(ingressStatus, status) {
			return ingressStatus, false
		}
		return setIngressStatus(ingressStatus, status), true
	})
}

// cleanIngressStatus removes all status fields for the given hostIp
func (c *kubernetesClients) cleanIngressStatus(ctx context.Context, ingress *v1.Ingress, hostIp net.IP) error {
	_, _, ok := findIngressStatus(ingress.Status.LoadBalancer.Ingress, hostIp.String())
	if !ok {
		return nil
	}

	return c.syncIngressStatus(ctx, ingress, func(ingressStatus []v1.IngressLoadBalancerIngress) ([]v1.IngressLoadBalancerIngress, bool) {
		log.Debug().Msgf("Cleaning ingress status for %s in namespace %s", ingress.Name, ingress.Namespace)
		_, i, ok := findIngressStatus(ingressStatus, hostIp.String())
		if !ok {
			return ingressStatus, false
		}
		return append(ingress.Status.LoadBalancer.Ingress[:i], ingress.Status.LoadBalancer.Ingress[i+1:]...), true
	})
}

// ingressPatchStatusFunc patches an ingress status and returns a boolean whether this needs to be synced to the kubernetes api.
// Usually false for this value makes only sense when the ingress state is already as desired.
type ingressPatchStatusFunc func([]v1.IngressLoadBalancerIngress) (patchedStatus []v1.IngressLoadBalancerIngress, doSync bool)

// syncIngressStatus syncs the ingress status to the kubernetes api.
func (c *kubernetesClients) syncIngressStatus(ctx context.Context, ingress *v1.Ingress, patchStatus ingressPatchStatusFunc) error {
	client := c.client.NetworkingV1().Ingresses(ingress.Namespace)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := client.Get(ctx, ingress.Name, v1Meta.GetOptions{})
		if err != nil {
			log.Debug().Err(err).Msgf("ingress update error when fetching current ingress state")
			return fmt.Errorf("ingress update error when fetching current ingress state: %v", err)
		}
		var needSync bool
		current.Status.LoadBalancer.Ingress, needSync = patchStatus(current.Status.LoadBalancer.Ingress)
		if !needSync {
			return nil
		}
		_, err = client.UpdateStatus(ctx, current, v1Meta.UpdateOptions{})
		if err != nil {
			log.Debug().Err(err).Msgf("ingress update error when saving updated ingress")
			return fmt.Errorf("ingress update error when saving updated ingress: %v", err)
		}
		return nil
	})
}

// either replaces the matching ingress status or (if none matches) appends the status
func setIngressStatus(status []v1.IngressLoadBalancerIngress, target *v1.IngressLoadBalancerIngress) []v1.IngressLoadBalancerIngress {
	for i, el := range status {
		if el.IP == target.IP {
			status[i] = *target
			return status
		}
	}
	return append(status, *target)
}

// findIngressStatus returns the ingress status with the matching ip address
func findIngressStatus(status []v1.IngressLoadBalancerIngress, hostIP string) (result *v1.IngressLoadBalancerIngress, index int, ok bool) {
	for i, el := range status {
		if el.IP == hostIP {
			return &el, i, true
		}
	}
	return nil, -1, false
}

// statusContained returns whether the list contains a status element. The ports array is checked on a per element basis (order-sensitive)
func statusContained(list []v1.IngressLoadBalancerIngress, el *v1.IngressLoadBalancerIngress) bool {
	listEl, _, ok := findIngressStatus(list, el.IP)
	return ok && statusEqual(listEl, el)
}

// statusEqual returns whether the two ingress status are equal. The ports array is checked on a per element basis (order-sensitive)
func statusEqual(el1 *v1.IngressLoadBalancerIngress, el2 *v1.IngressLoadBalancerIngress) bool {
	if el1.Hostname != el2.Hostname || el1.IP != el2.IP || len(el1.Ports) != len(el2.Ports) {
		return false
	}
	// we set the ports ourselves so order is fixed
	for i, port1 := range el1.Ports {
		port2 := el2.Ports[i]
		if port1.Port != port2.Port || port1.Protocol != port2.Protocol ||
			(port1.Error != nil && port2.Error != nil && *port1.Error != *port2.Error) ||
			((port1.Error == nil && port2.Error != nil) || (port1.Error != nil && port2.Error == nil)) {
			return false
		}
		return false
	}
	return true
}

// setupInformers setups and start all internal informers and sets the refetchState function as handler for AddFunc, UpdateFunc, DeleteFunc.
func (c *kubernetesClients) startInformers(ctx context.Context) error {
	if err := c.setupInformer(ctx, c.IngressInformer.Informer(), true); err != nil {
		return fmt.Errorf("failed to setup ingress informer: %v", err)
	}
	if err := c.setupInformer(ctx, c.ServiceInformer.Informer(), true); err != nil {
		return fmt.Errorf("failed to setup services informer: %v", err)
	}
	if err := c.setupInformer(ctx, c.SecretInformer.Informer(), false); err != nil {
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
	log.Debug().Msg("k8s update called")
	if !c.addUpdDelCallbackQueued.CompareAndSwap(false, true) {
		log.Debug().Msg("k8s update already queued")
		return
	}
	log.Debug().Msg("k8s update waits for k8s informers to sync")
	c.waitForSync(ctx)
	c.addUpdDelCallbackMu.Lock()
	defer c.addUpdDelCallbackMu.Unlock()
	c.addUpdDelCallbackQueued.Store(false)
	log.Debug().Msg("signalling k8s update")
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
