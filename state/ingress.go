package state

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	v1Core "k8s.io/api/core/v1"
	v1Net "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"net"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sync"

	_ "k8s.io/apimachinery/pkg/fields"               // Required for Watching
	_ "k8s.io/apimachinery/pkg/types"                // Required for Watching
	_ "sigs.k8s.io/controller-runtime/pkg/builder"   // Required for Watching
	_ "sigs.k8s.io/controller-runtime/pkg/handler"   // Required for Watching
	_ "sigs.k8s.io/controller-runtime/pkg/predicate" // Required for Watching
	_ "sigs.k8s.io/controller-runtime/pkg/reconcile" // Required for Watching
	_ "sigs.k8s.io/controller-runtime/pkg/source"    // Required for Watching
)

// IngressReconciler holds the main logic of the ingress controller regarding state updating
type IngressReconciler struct {
	k8sClients                *kubernetesClients
	ingressStateLock          sync.RWMutex
	ingressState              map[types.NamespacedName]*v1Net.Ingress
	ingressProcessedStateChan chan IngressState
	ingressClassName          string
	hostIp                    net.IP
	manager                   ctrl.Manager
}

// New creates a new Kubernetes Ingress reconsiler and registers it with the manager.
// The hostIp is an optional argument. If and only if it is set the ingress status is updated.
func New(mgr ctrl.Manager, ingressClassName string, hostIp net.IP) (*IngressReconciler, error) {
	k8sClients, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("error constructing k8s clients from manager config: %v", err)
	}
	r := &IngressReconciler{
		ingressClassName:          ingressClassName,
		ingressState:              make(map[types.NamespacedName]*v1Net.Ingress),
		ingressProcessedStateChan: make(chan IngressState),
		hostIp:                    hostIp,
		k8sClients:                newKubernetesClients(k8sClients),
		manager:                   mgr,
	}
	return r, ctrl.NewControllerManagedBy(mgr).
		For(&v1Net.Ingress{}).
		Watches(&source.Kind{Type: &v1Core.Secret{}},
			handler.EnqueueRequestsFromMapFunc(r.findIngressForSecret),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Watches(&source.Kind{Type: &v1Core.Service{}},
			handler.EnqueueRequestsFromMapFunc(r.findIngressForService),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		Complete(r)
}

// GetStateChan returns a read-only channel that carries the current state
func (r *IngressReconciler) GetStateChan() <-chan IngressState {
	return r.ingressProcessedStateChan
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.1/pkg/reconcile
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log.Debug().Msgf("reconciling ingress: %v", req)
	ingress, err := r.k8sClients.client.NetworkingV1().Ingresses(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{Requeue: true}, fmt.Errorf("error fetching ingress state: %v", err)
	}
	r.ingressStateLock.Lock()
	defer r.ingressStateLock.Unlock()
	if apierrors.IsNotFound(err) {
		log.Debug().Msgf("reconcile deleting ingress reference: %v", req)
		delete(r.ingressState, req.NamespacedName)
	} else {
		if ingress != nil && ((ingress.Spec.IngressClassName != nil && *ingress.Spec.IngressClassName != r.ingressClassName) ||
			(ingress.Spec.IngressClassName == nil && ingress.Annotations["kubernetes.io/ingress.class"] != r.ingressClassName)) {
			log.Debug().Msgf("reconciling ignoring ingress due to class-name: %v", req)
			return ctrl.Result{}, nil
		}
		log.Debug().Msgf("reconcile adding/updating ingress: %v", req)
		currentIngress, ok := r.ingressState[req.NamespacedName]
		if ok && reflect.DeepEqual(currentIngress.Spec, ingress.Spec) {
			// already processed, nothing to do
			return ctrl.Result{}, nil
		}
		r.ingressState[req.NamespacedName] = ingress.DeepCopy()
	}

	processedState, updates := r.processState()
	r.ingressProcessedStateChan <- processedState
	errors := r.updateStatus(ctx, updates)
	for _, err := range errors {
		// errors are missing values in the referenced services/secrets, no need to retry as we watch those resources
		log.Error().Err(err).Msg("failed to update ingress status")
	}
	return ctrl.Result{}, nil
}

// Start sets up the informers and the controller with the Manager, blocks till the context is cancelled or an error occurs.
func (r *IngressReconciler) Start(ctx context.Context) error {
	if err := r.k8sClients.startInformers(ctx); err != nil {
		return fmt.Errorf("failed to start kubernetes informers: %v", err)
	}
	r.k8sClients.waitForSync(ctx)

	return r.manager.Start(ctx)
}

func (r *IngressReconciler) findIngressForSecret(secret client.Object) []reconcile.Request {
	log.Debug().Msgf("watch triggered from secret %s in namespace %s", secret.GetName(), secret.GetNamespace())
	r.ingressStateLock.RLock()
	defer r.ingressStateLock.RUnlock()
	requests := make([]reconcile.Request, 0)
	for _, el := range r.ingressState {
		if el.Namespace != secret.GetNamespace() {
			continue
		}
		if referencesSecret(el, secret) {
			log.Debug().Msgf("reconcile queued due to secret update for ingress %s in namespace %s", el.Name, el.Namespace)
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      el.Name,
				Namespace: el.Namespace,
			}})
		}
	}
	return requests
}

// referencesSecret returns whether the ingress references the given secret
func referencesSecret(el *v1Net.Ingress, secret client.Object) bool {
	if el == nil {
		return false
	}
	for _, tls := range el.Spec.TLS {
		if tls.SecretName == secret.GetName() {
			return true
		}
	}
	return false
}

func (r *IngressReconciler) findIngressForService(service client.Object) []reconcile.Request {
	log.Debug().Msgf("watch triggered from service %s in namespace %s", service.GetName(), service.GetNamespace())
	r.ingressStateLock.RLock()
	defer r.ingressStateLock.RUnlock()
	requests := make([]reconcile.Request, 0)
	for _, el := range r.ingressState {
		if el.Namespace != service.GetNamespace() {
			continue
		}
		if referencesService(el, service) {
			log.Debug().Msgf("reconcile queued due to service update for ingress %s in namespace %s", el.Name, el.Namespace)
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      el.Name,
				Namespace: el.Namespace,
			}})
		}
	}
	return requests
}

// referencedService returns whether the ingress references the given service
func referencesService(el *v1Net.Ingress, service client.Object) bool {
	if el == nil {
		return false
	}
	for _, rule := range el.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			if path.Backend.Service == nil {
				continue
			}
			if path.Backend.Service.Name == service.GetName() {
				return true
			}
		}
	}
	return false
}

// CleanIngressStatus is supposed to be called during shutdown and removes all ingress status entries set by this instance.
// The internal state channel is not updated.
func (r *IngressReconciler) CleanIngressStatus(ctx context.Context) []error {
	errors := make([]error, 0)
	errChan := make(chan error)
	defer close(errChan) // to stop the error collection goroutine
	go func() {
		for err := range errChan {
			errors = append(errors, err)
		}
	}()

	var wg sync.WaitGroup
	r.ingressStateLock.Lock()
	defer r.ingressStateLock.Unlock()
	wg.Add(len(r.ingressState))
	for _, el := range r.ingressState {
		go func(ingress *v1Net.Ingress) {
			err := r.k8sClients.cleanIngressStatus(ctx, ingress, r.hostIp)
			if err != nil {
				errChan <- fmt.Errorf("could not clean ingress status: %v", err)
			}
			wg.Done()
		}(el)
	}
	wg.Wait()

	return errors
}
