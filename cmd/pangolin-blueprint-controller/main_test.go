package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/hirrrooo/pangolin-blueprint-controller/internal/blueprint"
	"github.com/hirrrooo/pangolin-blueprint-controller/internal/controller"
)

func TestHealthServer(t *testing.T) {
	factory := informers.NewSharedInformerFactory(fake.NewClientset(), 0)
	serviceController, err := controller.New(factory.Core().V1().Services().Informer(), factory.Core().V1().Secrets().Informer(), "pangolin-policies", filepath.Join(t.TempDir(), "blueprint.yaml"), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := healthServer(":0", serviceController)

	tests := []struct {
		path       string
		wantStatus int
	}{
		{path: "/healthz", wantStatus: http.StatusOK},
		{path: "/readyz", wantStatus: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		if response.Code != test.wantStatus {
			t.Errorf("%s status=%d, want %d", test.path, response.Code, test.wantStatus)
		}
	}
}

func TestPolicyInformerIsNamespaceAndLabelScoped(t *testing.T) {
	policy := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "members",
			Namespace: "pangolin-policies",
			Labels:    map[string]string{blueprint.PolicySecretLabel: blueprint.PolicySecretLabelValue},
		},
		Type: blueprint.PolicySecretType,
	}
	newtAuth := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: blueprint.ReservedNewtSecretName, Namespace: "pangolin-policies"},
	}
	otherNamespace := policy.DeepCopy()
	otherNamespace.Name = "other"
	otherNamespace.Namespace = "other-namespace"
	client := fake.NewClientset(policy, newtAuth, otherNamespace)
	serviceInformer, policyInformer, err := newInformers(client, "pangolin-policies")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go serviceInformer.Run(ctx.Done())
	go policyInformer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), serviceInformer.HasSynced, policyInformer.HasSynced) {
		t.Fatal("informer caches did not synchronize")
	}

	objects := policyInformer.GetStore().List()
	if len(objects) != 1 || objects[0].(*corev1.Secret).Name != "members" {
		t.Fatalf("policy cache contained unexpected Secrets: %#v", objects)
	}
	sawList := false
	sawWatch := false
	for _, action := range client.Actions() {
		if action.GetResource().Resource != "secrets" {
			continue
		}
		if action.GetNamespace() != "pangolin-policies" {
			t.Fatalf("Secret request escaped policy namespace: %#v", action)
		}
		if action.GetVerb() != "list" && action.GetVerb() != "watch" {
			t.Fatalf("unexpected Secret API verb %q", action.GetVerb())
		}
		var selector string
		switch typed := action.(type) {
		case k8stesting.ListAction:
			sawList = true
			selector = typed.GetListRestrictions().Labels.String()
		case k8stesting.WatchAction:
			sawWatch = true
			selector = typed.GetWatchRestrictions().Labels.String()
		}
		if selector != blueprint.PolicySecretLabel+"="+blueprint.PolicySecretLabelValue {
			t.Fatalf("Secret request has selector %q", selector)
		}
	}
	if !sawList || !sawWatch {
		t.Fatalf("expected Secret list and watch actions, saw list=%v watch=%v", sawList, sawWatch)
	}
}

func TestNewInformersRejectsInvalidPolicyNamespace(t *testing.T) {
	if _, _, err := newInformers(fake.NewClientset(), "Not Valid"); err == nil {
		t.Fatal("expected invalid policy namespace error")
	}
}

func TestKubernetesConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	content := []byte(`apiVersion: v1
kind: Config
clusters:
  - name: test
    cluster:
      server: https://kubernetes.example.test
contexts:
  - name: test
    context:
      cluster: test
      user: test
current-context: test
users:
  - name: test
    user:
      token: test-token
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := kubernetesConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "https://kubernetes.example.test" {
		t.Fatalf("host=%q", config.Host)
	}
}

func TestNewLoggerRejectsInvalidLevel(t *testing.T) {
	if _, err := newLogger("verbose"); err == nil {
		t.Fatal("expected invalid log level error")
	}
	if logger, err := newLogger("debug"); err != nil || logger == nil {
		t.Fatalf("logger=%v err=%v", logger, err)
	}
}
