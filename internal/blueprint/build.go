package blueprint

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
)

type ServiceError struct {
	Namespace     string
	Name          string
	Err           error
	ResourceID    string
	ConflictsWith string
}

func (e ServiceError) Error() string {
	return fmt.Sprintf("Service %s/%s: %v", e.Namespace, e.Name, e.Err)
}

func Build(services []*corev1.Service) (Blueprint, []ServiceError) {
	sort.Slice(services, func(i, j int) bool {
		return services[i].Namespace+"/"+services[i].Name < services[j].Namespace+"/"+services[j].Name
	})

	result := Blueprint{PublicResources: make(map[string]PublicResource)}
	owners := make(map[string]*corev1.Service)
	conflicts := make(map[string]bool)
	var serviceErrors []ServiceError

	for _, service := range services {
		id, resource, optedIn, err := FromService(service)
		if err != nil {
			serviceErrors = append(serviceErrors, ServiceError{Namespace: service.Namespace, Name: service.Name, Err: err})
			continue
		}
		if !optedIn {
			continue
		}
		if owner, exists := owners[id]; exists {
			delete(result.PublicResources, id)
			conflicts[id] = true
			serviceErrors = append(serviceErrors, ServiceError{
				Namespace:     service.Namespace,
				Name:          service.Name,
				Err:           fmt.Errorf("resource ID %q is already owned by Service %s/%s", id, owner.Namespace, owner.Name),
				ResourceID:    id,
				ConflictsWith: owner.Namespace + "/" + owner.Name,
			})
			continue
		}
		owners[id] = service
		if !conflicts[id] {
			result.PublicResources[id] = resource
		}
	}
	return result, serviceErrors
}

func Marshal(value Blueprint) ([]byte, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal blueprint: %w", err)
	}
	return data, nil
}
