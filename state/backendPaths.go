package state

import (
	"fmt"

	"github.com/rs/zerolog/log"
	v1Net "k8s.io/api/networking/v1"
	v1CoreListers "k8s.io/client-go/listers/core/v1"
)

// getBackendPaths collects for all services referenced in the ingresses the relevant ports and maps the ingress rules to the referenced hosts.
func getBackendPaths(serviceLister v1CoreListers.ServiceLister, ingresses []*v1Net.Ingress) BackendPaths {
	result := make(map[string][]*PathConfig)
	for _, ingress := range ingresses {
		for _, rule := range ingress.Spec.Rules {
			if rule.HTTP != nil {
				hostRules, ok := result[rule.Host]
				if !ok {
					hostRules = make([]*PathConfig, 0)
				}
				for _, path := range rule.HTTP.Paths {
					ingressPathConfig := &PathConfig{
						PathType:    path.PathType,
						Path:        path.Path,
						Namespace:   ingress.Namespace,
						ServiceName: path.Backend.Service.Name,
						ServicePort: path.Backend.Service.Port.Number,
					}
					err := updatePortFromService(serviceLister, ingressPathConfig, path.Backend.Service.Port.Name)
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
func updatePortFromService(serviceLister v1CoreListers.ServiceLister, config *PathConfig, servicePortName string) error {
	if config.ServicePort == 0 && servicePortName == "" {
		return fmt.Errorf("invalid config for path %s. Backend service does contain neither port name nor port number", config.Path)
	}
	svc, err := serviceLister.Services(config.Namespace).Get(config.ServiceName)
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
