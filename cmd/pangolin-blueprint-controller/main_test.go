package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/hirrrooo/pangolin-blueprint-controller/internal/controller"
)

func TestHealthServer(t *testing.T) {
	factory := informers.NewSharedInformerFactory(fake.NewClientset(), 0)
	serviceController, err := controller.New(factory.Core().V1().Services().Informer(), filepath.Join(t.TempDir(), "blueprint.yaml"), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
