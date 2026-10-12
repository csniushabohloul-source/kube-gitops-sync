package main

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDecodeAndSyncManifests_NamespaceIsolation(t *testing.T) {
	manifestYAML := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-custom-ns
  namespace: custom-space
spec:
  replicas: 1
  selector:
    matchLabels:
      app: custom
  template:
    metadata:
      labels:
        app: custom
    spec:
      containers:
      - name: web
        image: nginx:1.20
---
# Document without explicit namespace falls back to default
apiVersion: v1
kind: Service
metadata:
  name: svc-default-ns
spec:
  selector:
    app: custom
  ports:
  - port: 80
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-another-ns
  namespace: another-space
spec:
  replicas: 1
  selector:
    matchLabels:
      app: another
  template:
    metadata:
      labels:
        app: another
    spec:
      containers:
      - name: web
        image: nginx:1.21
`)

	client := fake.NewSimpleClientset()
	defaultNS := "default"

	results, err := DecodeAndSyncManifests(context.Background(), client, manifestYAML, defaultNS, false)
	if err != nil {
		t.Fatalf("unexpected error decoding manifests: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].Name != "app-custom-ns" || results[0].Namespace != "custom-space" {
		t.Errorf("doc 1: expected app-custom-ns in custom-space, got %s in %s", results[0].Name, results[0].Namespace)
	}

	if results[1].Name != "svc-default-ns" || results[1].Namespace != "default" {
		t.Errorf("doc 2: expected svc-default-ns in default namespace, got %s in %s", results[1].Name, results[1].Namespace)
	}

	if results[2].Name != "app-another-ns" || results[2].Namespace != "another-space" {
		t.Errorf("doc 3: expected app-another-ns in another-space, got %s in %s", results[2].Name, results[2].Namespace)
	}

	// Verify resources exist in their respective namespaces in the fake client
	_, err = client.AppsV1().Deployments("custom-space").Get(context.Background(), "app-custom-ns", metav1.GetOptions{})
	if err != nil {
		t.Errorf("deployment app-custom-ns not found in custom-space: %v", err)
	}

	_, err = client.CoreV1().Services("default").Get(context.Background(), "svc-default-ns", metav1.GetOptions{})
	if err != nil {
		t.Errorf("service svc-default-ns not found in default namespace: %v", err)
	}

	_, err = client.AppsV1().Deployments("another-space").Get(context.Background(), "app-another-ns", metav1.GetOptions{})
	if err != nil {
		t.Errorf("deployment app-another-ns not found in another-space: %v", err)
	}
}

func TestSyncDeployment_Lifecycle(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()
	namespace := "default"

	replicas := int32(2)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "frontend",
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "frontend"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "frontend"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "web", Image: "nginx:1.19"},
					},
				},
			},
		},
	}

	// Initial creation
	action, err := syncDeployment(ctx, client, dep, namespace, false)
	if err != nil {
		t.Fatalf("syncDeployment create error: %v", err)
	}
	if action != ActionCreated {
		t.Errorf("expected ActionCreated, got %v", action)
	}

	created, err := client.AppsV1().Deployments(namespace).Get(ctx, "frontend", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to retrieve created deployment: %v", err)
	}
	if created.Spec.Template.Spec.Containers[0].Image != "nginx:1.19" {
		t.Errorf("expected image nginx:1.19, got %s", created.Spec.Template.Spec.Containers[0].Image)
	}

	// Identical spec should report unchanged
	action, err = syncDeployment(ctx, client, dep, namespace, false)
	if err != nil {
		t.Fatalf("syncDeployment unchanged error: %v", err)
	}
	if action != ActionUnchanged {
		t.Errorf("expected ActionUnchanged, got %v", action)
	}

	// Spec update
	updatedDep := dep.DeepCopy()
	updatedDep.Spec.Template.Spec.Containers[0].Image = "nginx:1.21"
	action, err = syncDeployment(ctx, client, updatedDep, namespace, false)
	if err != nil {
		t.Fatalf("syncDeployment update error: %v", err)
	}
	if action != ActionUpdated {
		t.Errorf("expected ActionUpdated, got %v", action)
	}

	gotUpdated, err := client.AppsV1().Deployments(namespace).Get(ctx, "frontend", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated deployment: %v", err)
	}
	if gotUpdated.Spec.Template.Spec.Containers[0].Image != "nginx:1.21" {
		t.Errorf("expected updated image nginx:1.21, got %s", gotUpdated.Spec.Template.Spec.Containers[0].Image)
	}
}

func TestSyncService_PreservesClusterIP(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctx := context.Background()
	namespace := "default"

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-service",
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "web"},
			Ports: []corev1.ServicePort{
				{Port: 80, Protocol: corev1.ProtocolTCP},
			},
		},
	}

	// Service creation
	action, err := syncService(ctx, client, svc, namespace, false)
	if err != nil {
		t.Fatalf("syncService create error: %v", err)
	}
	if action != ActionCreated {
		t.Errorf("expected ActionCreated, got %v", action)
	}

	// Simulate cluster-assigned ClusterIP
	existing, err := client.CoreV1().Services(namespace).Get(ctx, "web-service", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get service: %v", err)
	}
	existing.Spec.ClusterIP = "10.96.0.42"
	_, err = client.CoreV1().Services(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("failed to inject ClusterIP: %v", err)
	}

	// Update service with a new port
	svcWithNewPort := svc.DeepCopy()
	svcWithNewPort.Spec.Ports = []corev1.ServicePort{
		{Port: 8080, Protocol: corev1.ProtocolTCP},
	}

	action, err = syncService(ctx, client, svcWithNewPort, namespace, false)
	if err != nil {
		t.Fatalf("syncService update error: %v", err)
	}
	if action != ActionUpdated {
		t.Errorf("expected ActionUpdated, got %v", action)
	}

	// Verify ClusterIP was preserved on update
	updatedSvc, err := client.CoreV1().Services(namespace).Get(ctx, "web-service", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get updated service: %v", err)
	}
	if updatedSvc.Spec.ClusterIP != "10.96.0.42" {
		t.Errorf("expected ClusterIP 10.96.0.42 to be preserved, got %s", updatedSvc.Spec.ClusterIP)
	}
	if len(updatedSvc.Spec.Ports) != 1 || updatedSvc.Spec.Ports[0].Port != 8080 {
		t.Errorf("expected updated port 8080, got %v", updatedSvc.Spec.Ports)
	}
}

func TestDryRunMode_NeverMutatesCluster(t *testing.T) {
	manifestYAML := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dry-run-deployment
spec:
  replicas: 1
  selector:
    matchLabels:
      app: dry
  template:
    metadata:
      labels:
        app: dry
    spec:
      containers:
      - name: dry
        image: nginx:latest
---
apiVersion: v1
kind: Service
metadata:
  name: dry-run-service
spec:
  ports:
  - port: 80
`)

	client := fake.NewSimpleClientset()
	defaultNS := "default"

	// Run with dryRun = true
	results, err := DecodeAndSyncManifests(context.Background(), client, manifestYAML, defaultNS, true)
	if err != nil {
		t.Fatalf("DecodeAndSyncManifests error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if !r.DryRun {
			t.Errorf("expected result DryRun to be true, got %v", r.DryRun)
		}
		if r.Action != ActionCreated {
			t.Errorf("expected simulated ActionCreated, got %v", r.Action)
		}
	}

	// Verify NO mutating actions occurred on fake client (only 'get' is allowed)
	for _, action := range client.Actions() {
		verb := action.GetVerb()
		if verb == "create" || verb == "update" || verb == "patch" || verb == "delete" {
			t.Fatalf("dry-run triggered mutating action: %s on %s", verb, action.GetResource().Resource)
		}
	}

	// Assert resources do NOT actually exist in the cluster
	_, err = client.AppsV1().Deployments(defaultNS).Get(context.Background(), "dry-run-deployment", metav1.GetOptions{})
	if err == nil {
		t.Fatal("dry-run should not have created Deployment in cluster")
	}

	_, err = client.CoreV1().Services(defaultNS).Get(context.Background(), "dry-run-service", metav1.GetOptions{})
	if err == nil {
		t.Fatal("dry-run should not have created Service in cluster")
	}
}

func TestDecodeAndSyncManifests_UnsupportedAndInvalid(t *testing.T) {
	// Manifest with an unsupported kind (ConfigMap) and valid Deployment
	mixedManifest := []byte(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
data:
  key: value
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: valid-dep
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test
  template:
    metadata:
      labels:
        app: test
    spec:
      containers:
      - name: test
        image: nginx
`)

	client := fake.NewSimpleClientset()
	results, err := DecodeAndSyncManifests(context.Background(), client, mixedManifest, "default", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First should be skipped gracefully
	if results[0].Kind != "ConfigMap" || results[0].Action != ActionSkipped {
		t.Errorf("expected ConfigMap to be skipped, got %v (%v)", results[0].Kind, results[0].Action)
	}

	// Second should be created
	if results[1].Kind != "Deployment" || results[1].Action != ActionCreated {
		t.Errorf("expected Deployment to be created, got %v (%v)", results[1].Kind, results[1].Action)
	}

	// Malformed YAML document should return error without panic
	malformed := []byte(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: broken
spec: [invalid: yaml: {{
`)

	_, err = DecodeAndSyncManifests(context.Background(), client, malformed, "default", false)
	if err == nil {
		t.Error("expected error on malformed YAML, got nil")
	}
}

func TestPrintSyncSummary(t *testing.T) {
	results := []ResourceResult{
		{
			Kind:      "Deployment",
			Name:      "web",
			Namespace: "default",
			Action:    ActionCreated,
			DryRun:    false,
		},
		{
			Kind:      "Service",
			Name:      "web-svc",
			Namespace: "default",
			Action:    ActionUpdated,
			DryRun:    true,
		},
		{
			Kind:      "Deployment",
			Name:      "broken",
			Namespace: "default",
			Action:    ActionSkipped,
			Error:     context.Canceled,
		},
	}

	// Simply ensure PrintSyncSummary executes cleanly without error or panic
	PrintSyncSummary(results)
}
