package blueprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
)

const (
	PolicySecretLabel      = "pangolin.net/public-policy"
	PolicySecretLabelValue = "true"
	PolicySecretType       = corev1.SecretType("pangolin.net/public-policy")
	ReservedNewtSecretName = "newt-auth"
)

var policySecretKeys = set(
	"name",
	"sso",
	"auto-login-idp",
	"sso-roles",
	"sso-users",
	"password",
	"pincode",
	"basic-auth-user",
	"basic-auth-password",
	"basic-auth-extended-compatibility",
	"email-whitelist-enabled",
	"whitelist-users",
	"apply-rules",
	"rules",
)

func ParsePolicySecret(secret *corev1.Secret) (PublicPolicy, error) {
	if secret == nil {
		return PublicPolicy{}, fmt.Errorf("policy Secret is nil")
	}
	if secret.Name == ReservedNewtSecretName {
		return PublicPolicy{}, fmt.Errorf("Secret name %q is reserved for Newt credentials", ReservedNewtSecretName)
	}
	if secret.Labels[PolicySecretLabel] != PolicySecretLabelValue {
		return PublicPolicy{}, fmt.Errorf("label %s=%q is required", PolicySecretLabel, PolicySecretLabelValue)
	}
	if secret.Type != PolicySecretType {
		return PublicPolicy{}, fmt.Errorf("Secret type must be %q", PolicySecretType)
	}
	for key := range secret.Data {
		if _, allowed := policySecretKeys[key]; !allowed {
			return PublicPolicy{}, fmt.Errorf("unsupported Secret key %q", key)
		}
	}

	if raw, exists := secret.Data["name"]; exists && !utf8.Valid(raw) {
		return PublicPolicy{}, fmt.Errorf("key %q must contain valid UTF-8 text", "name")
	}
	name := strings.TrimSpace(string(secret.Data["name"]))
	if name == "" {
		return PublicPolicy{}, fmt.Errorf("key %q is required and must be non-empty", "name")
	}

	sso, _, err := policyBool(secret.Data, "sso", true)
	if err != nil {
		return PublicPolicy{}, err
	}
	autoLoginIDP, err := policyPositiveInt(secret.Data, "auto-login-idp")
	if err != nil {
		return PublicPolicy{}, err
	}
	ssoRoles, err := policyStringList(secret.Data, "sso-roles")
	if err != nil {
		return PublicPolicy{}, err
	}
	for _, role := range ssoRoles {
		if strings.EqualFold(role, "Admin") {
			return PublicPolicy{}, fmt.Errorf("key %q must not include the Admin role", "sso-roles")
		}
	}
	ssoUsers, err := policyStringList(secret.Data, "sso-users")
	if err != nil {
		return PublicPolicy{}, err
	}
	if !sso && (autoLoginIDP != nil || len(ssoRoles) > 0 || len(ssoUsers) > 0) {
		return PublicPolicy{}, fmt.Errorf("SSO settings require key %q to be true", "sso")
	}

	password, passwordSet, err := nonEmptyPolicyValue(secret.Data, "password")
	if err != nil {
		return PublicPolicy{}, err
	}
	pin, pinSet, err := nonEmptyPolicyValue(secret.Data, "pincode")
	if err != nil {
		return PublicPolicy{}, err
	}
	if pinSet && !sixDigitPIN(pin) {
		return PublicPolicy{}, fmt.Errorf("key %q must contain exactly six digits", "pincode")
	}

	basicAuth, err := parsePolicyBasicAuth(secret.Data)
	if err != nil {
		return PublicPolicy{}, err
	}
	emailWhitelistEnabled, _, err := policyBool(secret.Data, "email-whitelist-enabled", false)
	if err != nil {
		return PublicPolicy{}, err
	}
	whitelistUsers, err := policyStringList(secret.Data, "whitelist-users")
	if err != nil {
		return PublicPolicy{}, err
	}
	if emailWhitelistEnabled && len(whitelistUsers) == 0 {
		return PublicPolicy{}, fmt.Errorf("key %q requires at least one whitelist user", "email-whitelist-enabled")
	}
	if !emailWhitelistEnabled && len(whitelistUsers) > 0 {
		return PublicPolicy{}, fmt.Errorf("key %q requires key %q to be true", "whitelist-users", "email-whitelist-enabled")
	}

	applyRules, _, err := policyBool(secret.Data, "apply-rules", false)
	if err != nil {
		return PublicPolicy{}, err
	}
	rules, err := policyRules(secret.Data)
	if err != nil {
		return PublicPolicy{}, err
	}
	if applyRules && len(rules) == 0 {
		return PublicPolicy{}, fmt.Errorf("key %q requires at least one rule", "apply-rules")
	}
	if !applyRules && len(rules) > 0 {
		return PublicPolicy{}, fmt.Errorf("key %q requires key %q to be true", "rules", "apply-rules")
	}
	if !sso && !passwordSet && !pinSet && basicAuth == nil && !emailWhitelistEnabled && !applyRules {
		return PublicPolicy{}, fmt.Errorf("policy must enable at least one authentication or rule mechanism")
	}

	return PublicPolicy{
		Name:                  name,
		SSO:                   sso,
		AutoLoginIDP:          autoLoginIDP,
		SSORoles:              ssoRoles,
		SSOUsers:              ssoUsers,
		Password:              password,
		PINCode:               pin,
		BasicAuth:             basicAuth,
		EmailWhitelistEnabled: emailWhitelistEnabled,
		WhitelistUsers:        whitelistUsers,
		ApplyRules:            applyRules,
		Rules:                 rules,
	}, nil
}

func parsePolicyBasicAuth(data map[string][]byte) (*BasicAuth, error) {
	user, userSet, err := nonEmptyPolicyValue(data, "basic-auth-user")
	if err != nil {
		return nil, err
	}
	password, passwordSet, err := nonEmptyPolicyValue(data, "basic-auth-password")
	if err != nil {
		return nil, err
	}
	if userSet != passwordSet {
		return nil, fmt.Errorf("keys %q and %q must be provided together", "basic-auth-user", "basic-auth-password")
	}
	compatibility, compatibilitySet, err := policyBool(data, "basic-auth-extended-compatibility", true)
	if err != nil {
		return nil, err
	}
	if compatibilitySet && !userSet {
		return nil, fmt.Errorf("key %q requires a complete basic-auth credential pair", "basic-auth-extended-compatibility")
	}
	if !userSet {
		return nil, nil
	}
	result := &BasicAuth{User: user, Password: password}
	if compatibilitySet {
		result.ExtendedCompatibility = &compatibility
	}
	return result, nil
}

func policyBool(data map[string][]byte, key string, fallback bool) (bool, bool, error) {
	raw, exists := data[key]
	if !exists {
		return fallback, false, nil
	}
	switch string(raw) {
	case "true":
		return true, true, nil
	case "false":
		return false, true, nil
	default:
		return false, true, fmt.Errorf("key %q must be true or false", key)
	}
}

func policyPositiveInt(data map[string][]byte, key string) (*int, error) {
	raw, exists := data[key]
	if !exists {
		return nil, nil
	}
	value, err := strconv.Atoi(string(raw))
	if err != nil || value < 1 {
		return nil, fmt.Errorf("key %q must be a positive integer", key)
	}
	return &value, nil
}

func policyStringList(data map[string][]byte, key string) ([]string, error) {
	raw, exists := data[key]
	if !exists {
		return nil, nil
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("key %q must be a JSON array of strings", key)
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("key %q must be a JSON array of strings", key)
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("key %q must not contain empty values", key)
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func policyRules(data map[string][]byte) ([]Rule, error) {
	raw, exists := data["rules"]
	if !exists {
		return nil, nil
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("key %q must be a JSON array of rules", "rules")
	}
	var rules []Rule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("key %q must be a JSON array of rules", "rules")
	}
	if err := validatePolicyRules(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func validatePolicyRules(rules []Rule) error {
	for index, rule := range rules {
		if _, ok := validRuleActions[rule.Action]; !ok {
			return fmt.Errorf("key %q rule %d has an invalid action", "rules", index)
		}
		if _, ok := validRuleMatches[rule.Match]; !ok {
			return fmt.Errorf("key %q rule %d has an invalid match", "rules", index)
		}
		if rule.Value == "" {
			return fmt.Errorf("key %q rule %d has an empty value", "rules", index)
		}
	}
	return nil
}

func nonEmptyPolicyValue(data map[string][]byte, key string) (string, bool, error) {
	raw, exists := data[key]
	if !exists {
		return "", false, nil
	}
	if len(raw) == 0 {
		return "", true, fmt.Errorf("key %q must be non-empty", key)
	}
	if !utf8.Valid(raw) {
		return "", true, fmt.Errorf("key %q must contain valid UTF-8 text", key)
	}
	return string(raw), true, nil
}

func sixDigitPIN(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
