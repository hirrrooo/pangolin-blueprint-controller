package deploy_test

import (
	"io"
	"os"
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestDeploymentKeepsSecretAccessNamespaceScoped(t *testing.T) {
	objects := deploymentObjects(t)
	clusterRole := convertObject[rbacv1.ClusterRole](t, objects["ClusterRole/pangolin-blueprint-controller"])
	if len(clusterRole.Rules) != 1 || !slices.Equal(clusterRole.Rules[0].Resources, []string{"services"}) || !slices.Equal(clusterRole.Rules[0].Verbs, []string{"get", "list", "watch"}) {
		t.Fatalf("unexpected cluster-wide permissions: %#v", clusterRole.Rules)
	}

	role := convertObject[rbacv1.Role](t, objects["Role/pangolin-blueprint-policy-reader"])
	if role.Namespace != "pangolin-policies" {
		t.Fatalf("policy Role namespace=%q", role.Namespace)
	}
	if len(role.Rules) != 1 || !slices.Equal(role.Rules[0].Resources, []string{"secrets"}) || !slices.Equal(role.Rules[0].Verbs, []string{"list", "watch"}) {
		t.Fatalf("unexpected policy permissions: %#v", role.Rules)
	}

	binding := convertObject[rbacv1.RoleBinding](t, objects["RoleBinding/pangolin-blueprint-policy-reader"])
	if binding.Namespace != "pangolin-policies" || len(binding.Subjects) != 1 || binding.Subjects[0].Name != "pangolin-newt" || binding.Subjects[0].Namespace != "pangolin" {
		t.Fatalf("unexpected policy RoleBinding: %#v", binding)
	}
}

func TestDeploymentPreservesTmpfsAndCredentialIsolation(t *testing.T) {
	objects := deploymentObjects(t)
	deployment := convertObject[appsv1.Deployment](t, objects["Deployment/pangolin-newt"])
	var blueprintVolume *corev1.Volume
	for index := range deployment.Spec.Template.Spec.Volumes {
		volume := &deployment.Spec.Template.Spec.Volumes[index]
		if volume.Name == "blueprint" {
			blueprintVolume = volume
			break
		}
	}
	if blueprintVolume == nil || blueprintVolume.EmptyDir == nil || blueprintVolume.EmptyDir.Medium != corev1.StorageMediumMemory {
		t.Fatalf("blueprint volume is not memory-backed: %#v", blueprintVolume)
	}

	var newt, controller *corev1.Container
	for index := range deployment.Spec.Template.Spec.Containers {
		container := &deployment.Spec.Template.Spec.Containers[index]
		switch container.Name {
		case "newt":
			newt = container
		case "blueprint-controller":
			controller = container
		}
	}
	if newt == nil || controller == nil {
		t.Fatalf("expected newt and controller containers: %#v", deployment.Spec.Template.Spec.Containers)
	}
	if len(newt.EnvFrom) != 1 || newt.EnvFrom[0].SecretRef == nil || newt.EnvFrom[0].SecretRef.Name != "newt-auth" {
		t.Fatalf("Newt credential Secret changed: %#v", newt.EnvFrom)
	}
	if len(controller.EnvFrom) != 0 {
		t.Fatalf("controller received environment Secrets: %#v", controller.EnvFrom)
	}
	if !slices.Contains(controller.Args, "--policy-namespace=pangolin-policies") {
		t.Fatalf("controller policy namespace is not configured: %#v", controller.Args)
	}
	for _, mount := range newt.VolumeMounts {
		if mount.Name == "blueprint" && !mount.ReadOnly {
			t.Fatal("Newt blueprint mount must remain read-only")
		}
	}
}

func deploymentObjects(t *testing.T) map[string]*unstructured.Unstructured {
	t.Helper()
	file, err := os.Open("deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := yaml.NewYAMLOrJSONDecoder(file, 4096)
	objects := make(map[string]*unstructured.Unstructured)
	for {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if object.GetKind() == "" {
			continue
		}
		objects[object.GetKind()+"/"+object.GetName()] = object
	}
	return objects
}

func convertObject[T any](t *testing.T, object *unstructured.Unstructured) *T {
	t.Helper()
	if object == nil {
		t.Fatal("required manifest object is missing")
	}
	result := new(T)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, result); err != nil {
		t.Fatal(err)
	}
	return result
}
