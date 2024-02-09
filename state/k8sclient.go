package state

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	v1 "k8s.io/api/networking/v1"
	v1Net "k8s.io/api/networking/v1"
	v1Meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"

	"k8s.io/client-go/kubernetes"
	v1ClientCore "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/retry"
	"net"
	"sync"
)

// kubernetesClients provides informers and ingress kubernetes clients for ingress updates.
type kubernetesClients struct {
	client        kubernetes.Interface
	ServiceLister v1ClientCore.ServiceLister
	SecretLister  v1ClientCore.SecretLister
	factories     []informers.SharedInformerFactory
}

// newKubernetesClients creates a new kubernetesClients struct. The ctx can be used to cancel the listening to updates from the Kubernetes API.
func newKubernetesClients(client kubernetes.Interface) *kubernetesClients {
	factoryService := informers.NewSharedInformerFactory(client, 0)
	factorySecrets := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(
		func(list *v1Meta.ListOptions) {
			list.FieldSelector = fields.OneTermEqualSelector("type", "kubernetes.io/tls").String()
		}))

	// we have to instantiate the informers once to register them
	factoryService.Core().V1().Services().Informer()
	factorySecrets.Core().V1().Secrets().Informer()
	clients := &kubernetesClients{
		client:        client,
		factories:     []informers.SharedInformerFactory{factoryService, factorySecrets},
		ServiceLister: factoryService.Core().V1().Services().Lister(),
		SecretLister:  factorySecrets.Core().V1().Secrets().Lister(),
	}
	return clients
}

// updateIngressStatus updates the ingress status and syncs the result with Kubernetes (if changes have occurred)
func (c *kubernetesClients) updateIngressStatus(ctx context.Context, ingress *v1.Ingress, updatedStatus *v1Net.IngressLoadBalancerIngress) error {
	currentStatus, _, ok := findIngressStatus(ingress.Status.LoadBalancer.Ingress, updatedStatus.IP)
	// we set the message for both ports equal so no need to differentiate here
	if ok && statusEqual(currentStatus, updatedStatus) {
		return nil
	}
	return c.syncIngressStatus(ctx, ingress, func(ingressStatus []v1.IngressLoadBalancerIngress) ([]v1.IngressLoadBalancerIngress, bool) {
		if statusContained(ingressStatus, updatedStatus) {
			return ingressStatus, false
		}
		log.Debug().Msgf("Setting/Updating ingress status for %s in namespace %s", ingress.Name, ingress.Namespace)
		return setIngressStatus(ingressStatus, updatedStatus), true
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
			return fmt.Errorf("ingress update error when fetching current ingress state: %w", err)
		}
		current = current.DeepCopy()
		var needSync bool
		current.Status.LoadBalancer.Ingress, needSync = patchStatus(current.Status.LoadBalancer.Ingress)
		if !needSync {
			return nil
		}
		_, err = client.UpdateStatus(ctx, current, v1Meta.UpdateOptions{})
		if err != nil {
			log.Debug().Err(err).Msgf("ingress update error when saving updated ingress")
			return fmt.Errorf("ingress update error when saving updated ingress: %w", err)
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

// statusContained returns whether the list contains a status element. The ports array is checked on a per-element basis (order-sensitive)
func statusContained(list []v1.IngressLoadBalancerIngress, el *v1.IngressLoadBalancerIngress) bool {
	listEl, _, ok := findIngressStatus(list, el.IP)
	return ok && statusEqual(listEl, el)
}

// statusEqual returns whether the two ingress status are equal. The ports array is checked on a per-element basis (order-sensitive)
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
	}
	return true
}

// startInforms starts all Informers
func (c *kubernetesClients) startInformers(ctx context.Context) error {
	for _, factory := range c.factories {
		factory.Start(ctx.Done())
	}
	return nil
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
