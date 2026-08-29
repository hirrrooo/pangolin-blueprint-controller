package blueprint

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFromServiceHTTP(t *testing.T) {
	service := testService(map[string]string{
		AnnotationPublic:         "true",
		AnnotationDomain:         "app.example.com",
		AnnotationPort:           "web",
		AnnotationMethod:         "https",
		AnnotationSSOEnabled:     "true",
		AnnotationSSORoles:       "Ops, Member",
		AnnotationWhitelistUsers: "admin@example.com",
		AnnotationRules:          `[{"action":"deny","match":"path","value":"/admin","priority":10}]`,
	})
	service.Spec.Ports = []corev1.ServicePort{{Name: "web", Port: 8080}, {Name: "metrics", Port: 9090}}

	id, resource, optedIn, err := FromService(service)
	if err != nil {
		t.Fatal(err)
	}
	if !optedIn || id != "apps--demo" {
		t.Fatalf("unexpected identity: optedIn=%v id=%q", optedIn, id)
	}
	if resource.FullDomain != "app.example.com" || resource.Mode != "http" {
		t.Fatalf("unexpected resource: %#v", resource)
	}
	if target := resource.Targets[0]; target.Hostname != "demo.apps.svc.cluster.local" || target.Port != 8080 || target.Method != "https" {
		t.Fatalf("unexpected target: %#v", target)
	}
	if resource.Auth == nil || resource.Auth.SSOEnabled == nil || !*resource.Auth.SSOEnabled {
		t.Fatalf("unexpected auth: %#v", resource.Auth)
	}
	if got := strings.Join(resource.Auth.SSORoles, ","); got != "Member,Ops" {
		t.Fatalf("roles were not normalized: %q", got)
	}
	if len(resource.Rules) != 1 || resource.Rules[0].Priority == nil || *resource.Rules[0].Priority != 10 {
		t.Fatalf("unexpected rules: %#v", resource.Rules)
	}
}

func TestFromServiceRawResource(t *testing.T) {
	service := testService(map[string]string{
		AnnotationPublic:    "true",
		AnnotationMode:      "tcp",
		AnnotationProxyPort: "8443",
	})

	_, resource, optedIn, err := FromService(service)
	if err != nil {
		t.Fatal(err)
	}
	if !optedIn || resource.ProxyPort != 8443 || resource.Targets[0].Method != "" {
		t.Fatalf("unexpected resource: %#v", resource)
	}
}

func TestFromServiceIgnoresUnannotatedService(t *testing.T) {
	_, _, optedIn, err := FromService(testService(nil))
	if err != nil || optedIn {
		t.Fatalf("expected ignored service, optedIn=%v err=%v", optedIn, err)
	}
}

func TestFromServiceValidation(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		ports       []corev1.ServicePort
		wantError   string
	}{
		{name: "missing domain", annotations: map[string]string{AnnotationPublic: "true"}, wantError: AnnotationDomain},
		{name: "invalid domain", annotations: map[string]string{AnnotationPublic: "true", AnnotationDomain: "https://app.example.com"}, wantError: "valid domain"},
		{name: "ambiguous port", annotations: map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com"}, ports: []corev1.ServicePort{{Name: "web", Port: 80}, {Name: "admin", Port: 81}}, wantError: AnnotationPort},
		{name: "unknown named port", annotations: map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com", AnnotationPort: "missing"}, wantError: "does not match"},
		{name: "unknown numeric port", annotations: map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com", AnnotationPort: "81"}, wantError: "does not match"},
		{name: "bad mode", annotations: map[string]string{AnnotationPublic: "true", AnnotationMode: "icmp"}, wantError: AnnotationMode},
		{name: "raw missing proxy port", annotations: map[string]string{AnnotationPublic: "true", AnnotationMode: "udp"}, wantError: AnnotationProxyPort},
		{name: "raw auth", annotations: map[string]string{AnnotationPublic: "true", AnnotationMode: "tcp", AnnotationProxyPort: "443", AnnotationSSOEnabled: "true"}, wantError: "only valid for HTTP"},
		{name: "invalid method", annotations: map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com", AnnotationMethod: "ftp"}, wantError: AnnotationMethod},
		{name: "invalid bool", annotations: map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com", AnnotationSSOEnabled: "sometimes"}, wantError: AnnotationSSOEnabled},
		{name: "invalid rules JSON", annotations: map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com", AnnotationRules: "["}, wantError: "JSON array"},
		{name: "invalid rule action", annotations: map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com", AnnotationRules: `[{"action":"block","match":"path","value":"/"}]`}, wantError: "invalid action"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := testService(test.annotations)
			if test.ports != nil {
				service.Spec.Ports = test.ports
			}
			_, _, optedIn, err := FromService(service)
			if !optedIn || err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("optedIn=%v err=%v, want error containing %q", optedIn, err, test.wantError)
			}
		})
	}
}

func TestBuildOmitsInvalidAndDuplicateResources(t *testing.T) {
	valid := testService(map[string]string{AnnotationPublic: "true", AnnotationDomain: "valid.example.com"})
	valid.Name = "valid"
	invalid := testService(map[string]string{AnnotationPublic: "true"})
	invalid.Name = "invalid"
	firstDuplicate := testService(map[string]string{AnnotationPublic: "true", AnnotationDomain: "one.example.com", AnnotationResourceID: "shared"})
	firstDuplicate.Name = "one"
	secondDuplicate := testService(map[string]string{AnnotationPublic: "true", AnnotationDomain: "two.example.com", AnnotationResourceID: "shared"})
	secondDuplicate.Name = "two"

	result, serviceErrors := Build([]*corev1.Service{secondDuplicate, invalid, valid, firstDuplicate})
	if len(result.PublicResources) != 1 {
		t.Fatalf("got resources %#v", result.PublicResources)
	}
	if _, ok := result.PublicResources["apps--valid"]; !ok {
		t.Fatal("valid resource missing")
	}
	if len(serviceErrors) != 2 {
		t.Fatalf("got %d errors: %#v", len(serviceErrors), serviceErrors)
	}
	collision := serviceErrors[1]
	if collision.ResourceID != "shared" || collision.ConflictsWith != "apps/one" || collision.Name != "two" {
		t.Fatalf("unexpected collision diagnostic: %#v", collision)
	}
}

func TestMarshalUsesPangolinFieldNames(t *testing.T) {
	service := testService(map[string]string{AnnotationPublic: "true", AnnotationDomain: "app.example.com"})
	result, serviceErrors := Build([]*corev1.Service{service})
	if len(serviceErrors) != 0 {
		t.Fatal(serviceErrors)
	}
	data, err := Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"public-resources:", "apps--demo:", "full-domain: app.example.com", "hostname: demo.apps.svc.cluster.local", "method: http"} {
		if !strings.Contains(text, expected) {
			t.Errorf("output missing %q:\n%s", expected, text)
		}
	}
}

func testService(annotations map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "apps", Annotations: annotations},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80}}},
	}
}
