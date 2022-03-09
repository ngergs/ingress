package state

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	v1Core "k8s.io/api/core/v1"
	v1Net "k8s.io/api/networking/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

type IngressStateManager struct {
	informFactoryGeneral    informers.SharedInformerFactory
	informFactoryTlsSecrets informers.SharedInformerFactory
	ingressClassName        string
	ingressStateChan        chan *IngressState
}

type IngressState struct {
	PathMap    map[string][]*IngressPathConfig // host->ingressPath
	TlsSecrets map[string]*v1Core.Secret       // host
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

	factoryService := informers.NewSharedInformerFactory(client, 0)
	factorySecrets := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTweakListOptions(
		func(list *v1.ListOptions) {
			list.FieldSelector = fields.OneTermEqualSelector("type", "kubernetes.io/tls").String()
		}))

	stateManager := &IngressStateManager{
		informFactoryGeneral:    factoryService,
		informFactoryTlsSecrets: factorySecrets,
		ingressClassName:        ingressClassName,
		ingressStateChan:        make(chan *IngressState),
	}

	// Start listening to relevant API objects
	informHandler := debounce(ctx, time.Duration(1)*time.Second, stateManager.recomputeState)
	go stateManager.startInformer(ctx, stateManager.informFactoryGeneral.Networking().V1().Ingresses().Informer(), informHandler)
	go stateManager.startInformer(ctx, stateManager.informFactoryGeneral.Core().V1().Services().Informer(), informHandler)
	go stateManager.startInformer(ctx, stateManager.informFactoryTlsSecrets.Core().V1().Secrets().Informer(), informHandler)

	return stateManager
}

func (stateManager *IngressStateManager) GetStateChan() <-chan *IngressState {
	return stateManager.ingressStateChan
}

func (stateManager *IngressStateManager) recomputeState() {
	ingresses, err := stateManager.informFactoryGeneral.Networking().V1().Ingresses().Lister().List(labels.Everything())
	if err != nil {
		log.Error().Err(err).Msg("error listening ingresses")
		return
	}
	ingresses = filterByIngressClass(ingresses, stateManager.ingressClassName)
	ingressState := &IngressState{
		PathMap:    stateManager.getPaths(ingresses),
		TlsSecrets: stateManager.getSecrets(ingresses),
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
	go informer.Run(ctx.Done())
}

func (stateManager *IngressStateManager) getSecrets(ingresses []*v1Net.Ingress) map[string]*v1Core.Secret {
	result := make(map[string]*v1Core.Secret)
	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.TLS {
			secret, err := stateManager.informFactoryTlsSecrets.Core().V1().Secrets().Lister().Secrets(ingress.Namespace).Get(rule.SecretName)
			if err != nil {
				log.Warn().Err(err).Msgf("Error getting ingress TLS certificate secret %s in namespace %s, skipping entry.",
					rule.SecretName, ingress.Namespace)
				continue
			}
			if secret.Type != v1Core.SecretTypeTLS {
				log.Warn().Msgf("Secret type missmatch, required kubernetes.io/tls, but found %s for secret %s in namespace %s, skipping entry.",
					secret.Type, secret.Name, secret.Namespace)
			}
			for _, host := range rule.Hosts {
				result[host] = secret
			}
		}
	}
	return result
}

func (stateManager *IngressStateManager) getPaths(ingresses []*v1Net.Ingress) map[string][]*IngressPathConfig {
	result := make(map[string][]*IngressPathConfig)
	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			if rule.HTTP != nil {
				hostRules, ok := result[rule.Host]
				if !ok {
					hostRules = make([]*IngressPathConfig, 0)
				}
				for _, path := range rule.HTTP.Paths {
					ingressPathConfig := &IngressPathConfig{
						Namespace: ingress.Namespace,
						Config:    &path,
					}
					err := stateManager.getServiceProperties(ingressPathConfig)
					if err != nil {
						log.Warn().Err(err).Msgf("Error getting service port skipping ingress entry.")
					} else {
						hostRules = append(hostRules, ingressPathConfig)
					}
				}
				result[rule.Host] = hostRules
			}
		}
	}
	return result
}

func (stateManager *IngressStateManager) getServiceProperties(config *IngressPathConfig) error {
	serviceName := config.Config.Backend.Service.Name
	portNumber := config.Config.Backend.Service.Port.Number
	portName := config.Config.Backend.Service.Port.Name
	if portNumber == 0 && portName == "" {
		return fmt.Errorf("invalid config for path %s. Backend service does contain neither port name nor port number", config.Config.Path)
	}
	svc, err := stateManager.informFactoryGeneral.Core().V1().Services().Lister().Services(config.Namespace).Get(serviceName)
	if err != nil {
		return err
	}

	// number takes precedence
	for _, svcPort := range svc.Spec.Ports {
		if svcPort.Port == portNumber {
			config.ServicePort = &svcPort
			return nil
		}
	}
	for _, svcPort := range svc.Spec.Ports {
		if svcPort.Name == portName {
			config.ServicePort = &svcPort
			return nil
		}
	}
	return fmt.Errorf("port name %s specified but not found in service %s", portName, serviceName)
}

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
