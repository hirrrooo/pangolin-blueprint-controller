package controller

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/hirrrooo/pangolin-blueprint-controller/internal/blueprint"
)

func TestControllerReconcilesAddUpdateAndDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "apps",
			Annotations: map[string]string{
				blueprint.AnnotationPublic: "true",
				blueprint.AnnotationDomain: "old.example.com",
			},
		},
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
	}
	client := fake.NewClientset(service)
	factory := informers.NewSharedInformerFactory(client, 0)
	output := filepath.Join(t.TempDir(), "blueprint.yaml")
	instance, err := New(factory.Core().V1().Services().Informer(), output, 10*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	runErrors := make(chan error, 1)
	go func() { runErrors <- instance.Run(ctx) }()

	assertFileContains(t, output, "full-domain: old.example.com")
	current, err := client.CoreV1().Services("apps").Get(ctx, "demo", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current.Annotations[blueprint.AnnotationDomain] = "new.example.com"
	if _, err := client.CoreV1().Services("apps").Update(ctx, current, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, output, "full-domain: new.example.com")

	if err := client.CoreV1().Services("apps").Delete(ctx, "demo", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	assertFileContains(t, output, "public-resources: {}")

	cancel()
	select {
	case err := <-runErrors:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("controller did not stop after cancellation")
	}
}

func TestReconcileLogsActionableServiceDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	factory := informers.NewSharedInformerFactory(fake.NewClientset(), 0)
	informer := factory.Core().V1().Services().Informer()
	instance, err := New(informer, filepath.Join(t.TempDir(), "blueprint.yaml"), time.Second, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}

	services := []*corev1.Service{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "plex",
				Namespace: "apps",
				Annotations: map[string]string{
					blueprint.AnnotationPublic: "true",
					blueprint.AnnotationDomain: "plex.example.com",
					blueprint.AnnotationPort:   "web",
				},
			},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80}, {Name: "pms", Port: 32400}}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana", Namespace: "apps", Annotations: map[string]string{
				blueprint.AnnotationPublic:     "true",
				blueprint.AnnotationDomain:     "grafana.example.com",
				blueprint.AnnotationResourceID: "dashboard",
			}},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80}}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "homer", Namespace: "apps", Annotations: map[string]string{
				blueprint.AnnotationPublic:     "true",
				blueprint.AnnotationDomain:     "homer.example.com",
				blueprint.AnnotationResourceID: "dashboard",
			}},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 80}}},
		},
	}
	for _, service := range services {
		if err := informer.GetStore().Add(service); err != nil {
			t.Fatal(err)
		}
	}
	if err := instance.reconcile(); err != nil {
		t.Fatal(err)
	}

	output := logs.String()
	for _, expected := range []string{
		`"msg":"skipping invalid Service"`,
		`"component":"blueprint"`,
		`"namespace":"apps"`,
		`"service":"plex"`,
		`available ports: \"http\" (80), \"pms\" (32400)`,
		`"msg":"blueprint resource ID collision"`,
		`"service":"homer"`,
		`"resource_id":"dashboard"`,
		`"conflicts_with":"apps/grafana"`,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("logs missing %q:\n%s", expected, output)
		}
	}
}

func assertFileContains(t *testing.T, path, expected string) {
	t.Helper()
	err := wait.PollUntilContextTimeout(context.Background(), 10*time.Millisecond, 3*time.Second, true, func(context.Context) (bool, error) {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return strings.Contains(string(data), expected), nil
	})
	if err != nil {
		data, _ := os.ReadFile(path)
		t.Fatalf("file never contained %q; current content:\n%s", expected, data)
	}
}
