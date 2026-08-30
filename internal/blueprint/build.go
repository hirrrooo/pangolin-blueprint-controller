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
	Policy        string
	ConflictsWith string
}

func (e ServiceError) Error() string {
	return fmt.Sprintf("Service %s/%s: %v", e.Namespace, e.Name, e.Err)
}

type PolicyError struct {
	Namespace string
	Name      string
	Err       error
}

func (e PolicyError) Error() string {
	return fmt.Sprintf("policy Secret %s/%s: %v", e.Namespace, e.Name, e.Err)
}

type policyResolution struct {
	policy PublicPolicy
	err    error
}

func Build(services []*corev1.Service, policySecrets []*corev1.Secret) (Blueprint, []ServiceError, []PolicyError) {
	sort.Slice(services, func(i, j int) bool {
		return services[i].Namespace+"/"+services[i].Name < services[j].Namespace+"/"+services[j].Name
	})

	result := Blueprint{
		PublicResources: make(map[string]PublicResource),
		PublicPolicies:  make(map[string]PublicPolicy),
	}
	policies, policyErrors := buildPolicyCatalog(policySecrets)
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
		if resource.Policy != "" {
			resolution, exists := policies[resource.Policy]
			if !exists {
				serviceErrors = append(serviceErrors, ServiceError{
					Namespace: service.Namespace,
					Name:      service.Name,
					Policy:    resource.Policy,
					Err:       fmt.Errorf("referenced policy %q is missing", resource.Policy),
				})
				continue
			}
			if resolution.err != nil {
				serviceErrors = append(serviceErrors, ServiceError{
					Namespace: service.Namespace,
					Name:      service.Name,
					Policy:    resource.Policy,
					Err:       fmt.Errorf("referenced policy %q is invalid", resource.Policy),
				})
				continue
			}
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
	for _, resource := range result.PublicResources {
		if resource.Policy != "" {
			result.PublicPolicies[resource.Policy] = policies[resource.Policy].policy
		}
	}
	return result, serviceErrors, policyErrors
}

func buildPolicyCatalog(secrets []*corev1.Secret) (map[string]policyResolution, []PolicyError) {
	sort.Slice(secrets, func(i, j int) bool {
		return secrets[i].Namespace+"/"+secrets[i].Name < secrets[j].Namespace+"/"+secrets[j].Name
	})
	catalog := make(map[string]policyResolution, len(secrets))
	var policyErrors []PolicyError
	for _, secret := range secrets {
		policy, err := ParsePolicySecret(secret)
		catalog[secret.Name] = policyResolution{policy: policy, err: err}
		if err != nil {
			policyErrors = append(policyErrors, PolicyError{
				Namespace: secret.Namespace,
				Name:      secret.Name,
				Err:       err,
			})
		}
	}
	return catalog, policyErrors
}

func Marshal(value Blueprint) ([]byte, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal blueprint: %w", err)
	}
	return data, nil
}
