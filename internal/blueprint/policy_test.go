package blueprint

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParsePolicySecretComplete(t *testing.T) {
	secret := validPolicySecret("authenticated-members")
	secret.Data = map[string][]byte{
		"name":                              []byte("Authenticated members"),
		"sso":                               []byte("true"),
		"auto-login-idp":                    []byte("7"),
		"sso-roles":                         []byte(`["Platform","Member"]`),
		"sso-users":                         []byte(`["user@example.com"]`),
		"password":                          []byte("shared-secret"),
		"pincode":                           []byte("012345"),
		"basic-auth-user":                   []byte("demo"),
		"basic-auth-password":               []byte("basic-secret"),
		"basic-auth-extended-compatibility": []byte("false"),
		"email-whitelist-enabled":           []byte("true"),
		"whitelist-users":                   []byte(`["*@example.com"]`),
		"apply-rules":                       []byte("true"),
		"rules":                             []byte(`[{"action":"deny","match":"country","value":"XX","priority":10,"enabled":true}]`),
	}

	policy, err := ParsePolicySecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Name != "Authenticated members" || !policy.SSO || policy.AutoLoginIDP == nil || *policy.AutoLoginIDP != 7 {
		t.Fatalf("unexpected policy identity: %#v", policy)
	}
	if strings.Join(policy.SSORoles, ",") != "Member,Platform" || policy.PINCode != "012345" {
		t.Fatalf("unexpected normalized policy: %#v", policy)
	}
	if policy.BasicAuth == nil || policy.BasicAuth.User != "demo" || policy.BasicAuth.Password != "basic-secret" {
		t.Fatalf("unexpected basic auth: %#v", policy.BasicAuth)
	}
	if policy.BasicAuth.ExtendedCompatibility == nil || *policy.BasicAuth.ExtendedCompatibility {
		t.Fatalf("unexpected extended compatibility: %#v", policy.BasicAuth)
	}
	if len(policy.Rules) != 1 || policy.Rules[0].Enabled == nil || !*policy.Rules[0].Enabled {
		t.Fatalf("unexpected rules: %#v", policy.Rules)
	}
	data, err := Marshal(Blueprint{
		PublicResources: map[string]PublicResource{},
		PublicPolicies:  map[string]PublicPolicy{"authenticated-members": policy},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"public-policies:", "pincode:", "basic-auth:", "extended-compatibility: false", "email-whitelist-enabled: true", "apply-rules: true", "enabled: true"} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("policy blueprint missing %q:\n%s", expected, data)
		}
	}
}

func TestParsePolicySecretDefaultsSSOToTrue(t *testing.T) {
	policy, err := ParsePolicySecret(validPolicySecret("members"))
	if err != nil {
		t.Fatal(err)
	}
	if !policy.SSO {
		t.Fatal("omitted sso key must use Pangolin's true default")
	}
}

func TestParsePolicySecretValidation(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*corev1.Secret)
		wantError string
	}{
		{name: "reserved name", mutate: func(secret *corev1.Secret) { secret.Name = ReservedNewtSecretName }, wantError: "reserved"},
		{name: "missing label", mutate: func(secret *corev1.Secret) { delete(secret.Labels, PolicySecretLabel) }, wantError: "label"},
		{name: "wrong type", mutate: func(secret *corev1.Secret) { secret.Type = corev1.SecretTypeOpaque }, wantError: "type"},
		{name: "missing display name", mutate: func(secret *corev1.Secret) { delete(secret.Data, "name") }, wantError: `key "name"`},
		{name: "unknown key", mutate: func(secret *corev1.Secret) { secret.Data["pasword"] = []byte("secret") }, wantError: "unsupported"},
		{name: "invalid sso", mutate: func(secret *corev1.Secret) { secret.Data["sso"] = []byte("sometimes") }, wantError: "true or false"},
		{name: "uppercase sso", mutate: func(secret *corev1.Secret) { secret.Data["sso"] = []byte("TRUE") }, wantError: "true or false"},
		{name: "admin role", mutate: func(secret *corev1.Secret) { secret.Data["sso-roles"] = []byte(`["Admin"]`) }, wantError: "Admin"},
		{name: "sso settings disabled", mutate: func(secret *corev1.Secret) {
			secret.Data["sso"] = []byte("false")
			secret.Data["sso-users"] = []byte(`["user@example.com"]`)
		}, wantError: "SSO settings"},
		{name: "invalid idp", mutate: func(secret *corev1.Secret) { secret.Data["auto-login-idp"] = []byte("zero") }, wantError: "positive integer"},
		{name: "non-UTF8 password", mutate: func(secret *corev1.Secret) { secret.Data["password"] = []byte{0xff} }, wantError: "UTF-8"},
		{name: "empty password", mutate: func(secret *corev1.Secret) { secret.Data["password"] = nil }, wantError: "non-empty"},
		{name: "short pin", mutate: func(secret *corev1.Secret) { secret.Data["pincode"] = []byte("12345") }, wantError: "six digits"},
		{name: "non-digit pin", mutate: func(secret *corev1.Secret) { secret.Data["pincode"] = []byte("12345x") }, wantError: "six digits"},
		{name: "basic user only", mutate: func(secret *corev1.Secret) { secret.Data["basic-auth-user"] = []byte("demo") }, wantError: "provided together"},
		{name: "basic password only", mutate: func(secret *corev1.Secret) { secret.Data["basic-auth-password"] = []byte("secret") }, wantError: "provided together"},
		{name: "compatibility without pair", mutate: func(secret *corev1.Secret) { secret.Data["basic-auth-extended-compatibility"] = []byte("true") }, wantError: "complete basic-auth"},
		{name: "invalid role list", mutate: func(secret *corev1.Secret) { secret.Data["sso-roles"] = []byte("Member") }, wantError: "JSON array"},
		{name: "null role list", mutate: func(secret *corev1.Secret) { secret.Data["sso-roles"] = []byte("null") }, wantError: "JSON array"},
		{name: "empty role", mutate: func(secret *corev1.Secret) { secret.Data["sso-roles"] = []byte(`[""]`) }, wantError: "empty values"},
		{name: "whitelist disabled", mutate: func(secret *corev1.Secret) { secret.Data["whitelist-users"] = []byte(`["*@example.com"]`) }, wantError: "email-whitelist-enabled"},
		{name: "whitelist empty", mutate: func(secret *corev1.Secret) { secret.Data["email-whitelist-enabled"] = []byte("true") }, wantError: "whitelist user"},
		{name: "rules disabled", mutate: func(secret *corev1.Secret) {
			secret.Data["rules"] = []byte(`[{"action":"deny","match":"country","value":"XX"}]`)
		}, wantError: "apply-rules"},
		{name: "rules empty", mutate: func(secret *corev1.Secret) { secret.Data["apply-rules"] = []byte("true") }, wantError: "at least one rule"},
		{name: "null rules", mutate: func(secret *corev1.Secret) {
			secret.Data["apply-rules"] = []byte("true")
			secret.Data["rules"] = []byte("null")
		}, wantError: "JSON array"},
		{name: "invalid rule", mutate: func(secret *corev1.Secret) {
			secret.Data["apply-rules"] = []byte("true")
			secret.Data["rules"] = []byte(`[{"action":"block","match":"country","value":"XX"}]`)
		}, wantError: "invalid action"},
		{name: "no effective mechanism", mutate: func(secret *corev1.Secret) { secret.Data["sso"] = []byte("false") }, wantError: "at least one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			secret := validPolicySecret("members")
			test.mutate(secret)
			_, err := ParsePolicySecret(secret)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err=%v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestParsePolicySecretErrorsRedactValues(t *testing.T) {
	for _, key := range []string{"sso", "auto-login-idp", "sso-roles", "rules"} {
		t.Run(key, func(t *testing.T) {
			secret := validPolicySecret("members")
			secret.Data[key] = []byte("DO-NOT-LOG-CREDENTIAL")
			_, err := ParsePolicySecret(secret)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if strings.Contains(err.Error(), "DO-NOT-LOG-CREDENTIAL") {
				t.Fatalf("error leaked Secret value: %v", err)
			}
		})
	}
	secret := validPolicySecret("members")
	secret.Data["apply-rules"] = []byte("true")
	secret.Data["rules"] = []byte(`[{"action":"DO-NOT-LOG-CREDENTIAL","match":"country","value":"XX"}]`)
	_, err := ParsePolicySecret(secret)
	if err == nil {
		t.Fatal("expected invalid rule error")
	}
	if strings.Contains(err.Error(), "DO-NOT-LOG-CREDENTIAL") {
		t.Fatalf("rule error leaked Secret value: %v", err)
	}
}

func TestBuildGeneratesReusablePublicPolicy(t *testing.T) {
	first := testService(map[string]string{
		AnnotationPublic: "true",
		AnnotationDomain: "one.example.com",
		AnnotationPolicy: "members",
	})
	first.Name = "one"
	second := testService(map[string]string{
		AnnotationPublic: "true",
		AnnotationDomain: "two.example.com",
		AnnotationPolicy: "members",
	})
	second.Name = "two"
	secret := validPolicySecret("members")
	secret.Data["password"] = []byte("POLICY-PASSWORD")

	result, serviceErrors, policyErrors := Build([]*corev1.Service{second, first}, []*corev1.Secret{secret})
	if len(serviceErrors) != 0 || len(policyErrors) != 0 {
		t.Fatalf("service errors=%v policy errors=%v", serviceErrors, policyErrors)
	}
	if len(result.PublicResources) != 2 || len(result.PublicPolicies) != 1 {
		t.Fatalf("unexpected blueprint: %#v", result)
	}
	if result.PublicResources["apps--one"].Policy != "members" || result.PublicResources["apps--two"].Policy != "members" {
		t.Fatalf("resources did not reference policy: %#v", result.PublicResources)
	}
	data, err := Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"public-policies:",
		"policy: members",
		"password: POLICY-PASSWORD",
		"email-whitelist-enabled: false",
		"apply-rules: false",
	} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("blueprint missing %q:\n%s", expected, data)
		}
	}
}

func TestBuildFailsClosedForMissingOrInvalidPolicy(t *testing.T) {
	service := testService(map[string]string{
		AnnotationPublic: "true",
		AnnotationDomain: "app.example.com",
		AnnotationPolicy: "members",
	})
	tests := []struct {
		name          string
		secrets       []*corev1.Secret
		wantPolicyErr bool
	}{
		{name: "missing"},
		{name: "invalid", secrets: func() []*corev1.Secret {
			secret := validPolicySecret("members")
			secret.Data["pincode"] = []byte("SECRET-MARKER")
			return []*corev1.Secret{secret}
		}(), wantPolicyErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, serviceErrors, policyErrors := Build([]*corev1.Service{service}, test.secrets)
			if len(result.PublicResources) != 0 || len(result.PublicPolicies) != 0 {
				t.Fatalf("protected resource was emitted without a valid policy: %#v", result)
			}
			if len(serviceErrors) != 1 || serviceErrors[0].Policy != "members" {
				t.Fatalf("unexpected service errors: %#v", serviceErrors)
			}
			if (len(policyErrors) != 0) != test.wantPolicyErr {
				t.Fatalf("unexpected policy errors: %#v", policyErrors)
			}
			for _, issue := range append([]error{serviceErrors[0].Err}, policyErrorValues(policyErrors)...) {
				if strings.Contains(issue.Error(), "SECRET-MARKER") {
					t.Fatalf("error leaked credential: %v", issue)
				}
			}
		})
	}
}

func TestBuildDoesNotEmitUnreferencedPolicy(t *testing.T) {
	service := testService(map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com"})
	result, serviceErrors, policyErrors := Build([]*corev1.Service{service}, []*corev1.Secret{validPolicySecret("members")})
	if len(serviceErrors) != 0 || len(policyErrors) != 0 {
		t.Fatalf("service errors=%v policy errors=%v", serviceErrors, policyErrors)
	}
	if len(result.PublicPolicies) != 0 {
		t.Fatalf("unreferenced policy was emitted: %#v", result.PublicPolicies)
	}
}

func policyErrorValues(policyErrors []PolicyError) []error {
	errors := make([]error, 0, len(policyErrors))
	for _, policyError := range policyErrors {
		errors = append(errors, policyError.Err)
	}
	return errors
}

func validPolicySecret(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "pangolin-policies",
			Labels:    map[string]string{PolicySecretLabel: PolicySecretLabelValue},
		},
		Type: PolicySecretType,
		Data: map[string][]byte{"name": []byte("Members")},
	}
}
