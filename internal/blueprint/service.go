package blueprint

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	AnnotationPublic         = "pangolin.net/public"
	AnnotationDomain         = "pangolin.net/domain"
	AnnotationMode           = "pangolin.net/mode"
	AnnotationPort           = "pangolin.net/port"
	AnnotationProxyPort      = "pangolin.net/proxy-port"
	AnnotationMethod         = "pangolin.net/method"
	AnnotationPolicy         = "pangolin.net/policy"
	AnnotationResourceID     = "pangolin.net/resource-id"
	AnnotationName           = "pangolin.net/name"
	AnnotationSSOEnabled     = "pangolin.net/sso-enabled"
	AnnotationSSORoles       = "pangolin.net/sso-roles"
	AnnotationSSOUsers       = "pangolin.net/sso-users"
	AnnotationWhitelistUsers = "pangolin.net/whitelist-users"
	AnnotationRules          = "pangolin.net/rules"
)

var (
	validRuleActions = set("allow", "deny", "pass")
	validRuleMatches = set("cidr", "path", "ip", "country", "asn", "region")
)

func FromService(service *corev1.Service) (string, PublicResource, bool, error) {
	annotations := service.GetAnnotations()
	if annotations[AnnotationPublic] != "true" {
		return "", PublicResource{}, false, nil
	}

	mode := valueOr(annotations[AnnotationMode], "http")
	if mode != "http" && mode != "tcp" && mode != "udp" {
		return "", PublicResource{}, true, fmt.Errorf("%s must be http, tcp, or udp", AnnotationMode)
	}

	port, err := servicePort(service, annotations[AnnotationPort])
	if err != nil {
		return "", PublicResource{}, true, err
	}

	id := valueOr(annotations[AnnotationResourceID], service.Namespace+"--"+service.Name)
	if strings.TrimSpace(id) != id || id == "" {
		return "", PublicResource{}, true, fmt.Errorf("%s must be non-empty without surrounding whitespace", AnnotationResourceID)
	}

	policy := annotations[AnnotationPolicy]
	if strings.TrimSpace(policy) != policy {
		return "", PublicResource{}, true, fmt.Errorf("%s must not contain surrounding whitespace", AnnotationPolicy)
	}
	if policy != "" {
		if policy == ReservedNewtSecretName {
			return "", PublicResource{}, true, fmt.Errorf("%s must not reference reserved Secret %q", AnnotationPolicy, ReservedNewtSecretName)
		}
		if problems := validation.IsDNS1123Subdomain(policy); len(problems) > 0 {
			return "", PublicResource{}, true, fmt.Errorf("%s is not a valid Secret name: %s", AnnotationPolicy, strings.Join(problems, ", "))
		}
	}

	resource := PublicResource{
		Name: valueOr(annotations[AnnotationName], service.Namespace+"/"+service.Name),
		Mode: mode,
		Targets: []Target{{
			Hostname: service.Name + "." + service.Namespace + ".svc.cluster.local",
			Port:     port,
		}},
	}

	if mode == "http" {
		resource.FullDomain = strings.TrimSpace(annotations[AnnotationDomain])
		if resource.FullDomain == "" {
			return "", PublicResource{}, true, fmt.Errorf("%s is required for HTTP resources", AnnotationDomain)
		}
		if problems := validation.IsDNS1123Subdomain(resource.FullDomain); len(problems) > 0 {
			return "", PublicResource{}, true, fmt.Errorf("%s is not a valid domain: %s", AnnotationDomain, strings.Join(problems, ", "))
		}
		resource.Targets[0].Method = valueOr(annotations[AnnotationMethod], "http")
		if resource.Targets[0].Method != "http" && resource.Targets[0].Method != "https" && resource.Targets[0].Method != "h2c" {
			return "", PublicResource{}, true, fmt.Errorf("%s must be http, https, or h2c", AnnotationMethod)
		}
		if _, ok := annotations[AnnotationProxyPort]; ok {
			return "", PublicResource{}, true, fmt.Errorf("%s is only valid for TCP or UDP resources", AnnotationProxyPort)
		}
		if policy != "" {
			if hasAuthAnnotations(annotations) || annotations[AnnotationRules] != "" {
				return "", PublicResource{}, true, fmt.Errorf("%s cannot be combined with direct auth or rules annotations", AnnotationPolicy)
			}
			resource.Policy = policy
		} else {
			resource.Auth, err = parseAuth(annotations)
			if err != nil {
				return "", PublicResource{}, true, err
			}
			resource.Rules, err = parseRules(annotations[AnnotationRules])
			if err != nil {
				return "", PublicResource{}, true, err
			}
		}
	} else {
		if annotations[AnnotationDomain] != "" || annotations[AnnotationPolicy] != "" || hasAuthAnnotations(annotations) || annotations[AnnotationRules] != "" || annotations[AnnotationMethod] != "" {
			return "", PublicResource{}, true, fmt.Errorf("domain, method, policy, auth, and rules annotations are only valid for HTTP resources")
		}
		resource.ProxyPort, err = parsePort(annotations[AnnotationProxyPort], AnnotationProxyPort)
		if err != nil {
			return "", PublicResource{}, true, err
		}
	}

	return id, resource, true, nil
}

func servicePort(service *corev1.Service, selector string) (int32, error) {
	ports := service.Spec.Ports
	if selector == "" {
		if len(ports) != 1 {
			return 0, fmt.Errorf("%s is required because the Service exposes %d ports; available ports: %s", AnnotationPort, len(ports), availableServicePorts(ports))
		}
		return ports[0].Port, nil
	}
	if number, err := strconv.ParseInt(selector, 10, 32); err == nil {
		for _, port := range ports {
			if port.Port == int32(number) {
				return port.Port, nil
			}
		}
		return 0, fmt.Errorf("port %q does not match a Service port; available ports: %s", selector, availableServicePorts(ports))
	}
	for _, port := range ports {
		if port.Name == selector {
			return port.Port, nil
		}
	}
	return 0, fmt.Errorf("port %q does not match a Service port name; available ports: %s", selector, availableServicePorts(ports))
}

func availableServicePorts(ports []corev1.ServicePort) string {
	if len(ports) == 0 {
		return "none"
	}
	available := make([]string, 0, len(ports))
	for _, port := range ports {
		if port.Name == "" {
			available = append(available, strconv.Itoa(int(port.Port)))
			continue
		}
		available = append(available, fmt.Sprintf("%q (%d)", port.Name, port.Port))
	}
	return strings.Join(available, ", ")
}

func parseAuth(annotations map[string]string) (*Auth, error) {
	if !hasAuthAnnotations(annotations) {
		return nil, nil
	}
	auth := &Auth{
		SSORoles:       csv(annotations[AnnotationSSORoles]),
		SSOUsers:       csv(annotations[AnnotationSSOUsers]),
		WhitelistUsers: csv(annotations[AnnotationWhitelistUsers]),
	}
	if raw, ok := annotations[AnnotationSSOEnabled]; ok {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%s must be true or false: %w", AnnotationSSOEnabled, err)
		}
		auth.SSOEnabled = &value
	}
	return auth, nil
}

func parseRules(raw string) ([]Rule, error) {
	if raw == "" {
		return nil, nil
	}
	var rules []Rule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, fmt.Errorf("%s must be a JSON array", AnnotationRules)
	}
	if err := validateRules(rules, AnnotationRules); err != nil {
		return nil, err
	}
	return rules, nil
}

func validateRules(rules []Rule, field string) error {
	for index, rule := range rules {
		if _, ok := validRuleActions[rule.Action]; !ok {
			return fmt.Errorf("%s rule %d has invalid action %q", field, index, rule.Action)
		}
		if _, ok := validRuleMatches[rule.Match]; !ok {
			return fmt.Errorf("%s rule %d has invalid match %q", field, index, rule.Match)
		}
		if rule.Value == "" {
			return fmt.Errorf("%s rule %d has an empty value", field, index)
		}
	}
	return nil
}

func parsePort(raw, annotation string) (int32, error) {
	if raw == "" {
		return 0, fmt.Errorf("%s is required", annotation)
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < 1 || value > 65535 {
		return 0, fmt.Errorf("%s must be an integer from 1 to 65535", annotation)
	}
	return int32(value), nil
}

func hasAuthAnnotations(annotations map[string]string) bool {
	for _, key := range []string{AnnotationSSOEnabled, AnnotationSSORoles, AnnotationSSOUsers, AnnotationWhitelistUsers} {
		if _, ok := annotations[key]; ok {
			return true
		}
	}
	return false
}

func csv(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	sort.Strings(result)
	return result
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func set(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
