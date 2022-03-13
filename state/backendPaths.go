package state

import (
	"fmt"

	"github.com/rs/zerolog/log"
	v1Net "k8s.io/api/networking/v1"
	v1CoreListers "k8s.io/client-go/listers/core/v1"
)

// getBackendPaths collects for all services referenced in the ingresses the relevant ports and maps the ingress rules to the referenced hosts.
func getBackendPaths(serviceLister v1CoreListers.ServiceLister, ingresses []*v1Net.Ingress) BackendPaths {
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
					err := updatePortFromService(serviceLister, ingressPathConfig)
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

// updatePortFromService uses the Kubernetes API to fetch the Service status for the service referenced in the ingress config.
// If this has finished without error the config.ServicePort property is guranteed to be set according to the current service spec.
func updatePortFromService(serviceLister v1CoreListers.ServiceLister, config *IngressPathConfig) error {
	serviceName := config.Config.Backend.Service.Name
	portNumber := config.Config.Backend.Service.Port.Number
	portName := config.Config.Backend.Service.Port.Name
	if portNumber == 0 && portName == "" {
		return fmt.Errorf("invalid config for path %s. Backend service does contain neither port name nor port number", config.Config.Path)
	}
	svc, err := serviceLister.Services(config.Namespace).Get(serviceName)
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
