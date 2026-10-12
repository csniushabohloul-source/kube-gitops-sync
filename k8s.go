package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// SyncAction represents the reconciliation outcome for a single Kubernetes resource.
type SyncAction string

const (
	ActionCreated   SyncAction = "CREATED"
	ActionUpdated   SyncAction = "UPDATED"
	ActionUnchanged SyncAction = "UNCHANGED"
	ActionSkipped   SyncAction = "SKIPPED"
)

// ResourceResult records the result of reconciling a single manifest document.
type ResourceResult struct {
	Kind      string
	Name      string
	Namespace string
	Action    SyncAction
	DryRun    bool
	Error     error
}

// GetKubernetesClient creates a Kubernetes clientset.
// It automatically detects whether the application is running inside a cluster
// or externally using a kubeconfig file.
func GetKubernetesClient(kubeconfigPath string) (kubernetes.Interface, error) {
	// Check if running inside a Kubernetes cluster
	if _, inside := os.LookupEnv("KUBERNETES_SERVICE_HOST"); inside {
		log.Println("Detected in-cluster environment, loading in-cluster configuration...")
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load in-cluster configuration: %w", err)
		}
		return kubernetes.NewForConfig(config)
	}

	// Outside cluster: build config from kubeconfig path
	if kubeconfigPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("unable to determine user home directory: %w", err)
		}
		kubeconfigPath = filepath.Join(home, ".kube", "config")
	}

	log.Printf("Loading kubeconfig from %s...", kubeconfigPath)
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig from %q: %w", kubeconfigPath, err)
	}

	return kubernetes.NewForConfig(config)
}

// DecodeAndSyncManifests decodes a multi-document Kubernetes YAML manifest and synchronizes
// each resource against the target cluster. Manifests without an explicit namespace
// use defaultNamespace.
func DecodeAndSyncManifests(
	ctx context.Context,
	client kubernetes.Interface,
	manifestData []byte,
	defaultNamespace string,
	dryRun bool,
) ([]ResourceResult, error) {
	if client == nil {
		return nil, errors.New("kubernetes client cannot be nil")
	}

	var results []ResourceResult
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifestData), 4096)

	docIndex := 0
	for {
		docIndex++
		var raw runtime.RawExtension
		err := decoder.Decode(&raw)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return results, fmt.Errorf("failed to decode YAML document #%d: %w", docIndex, err)
		}

		trimmed := bytes.TrimSpace(raw.Raw)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			// Skip empty documents or separators
			continue
		}

		obj, gvk, err := scheme.Codecs.UniversalDeserializer().Decode(raw.Raw, nil, nil)
		if err != nil {
			log.Printf("Warning: document #%d could not be decoded: %v", docIndex, err)
			results = append(results, ResourceResult{
				Kind:   "Unknown",
				Action: ActionSkipped,
				Error:  fmt.Errorf("decode error: %w", err),
			})
			continue
		}

		// Independent namespace determination for each document
		targetNamespace := defaultNamespace

		switch res := obj.(type) {
		case *appsv1.Deployment:
			if res.Namespace != "" {
				targetNamespace = res.Namespace
			}
			action, syncErr := syncDeployment(ctx, client, res, targetNamespace, dryRun)
			results = append(results, ResourceResult{
				Kind:      "Deployment",
				Name:      res.Name,
				Namespace: targetNamespace,
				Action:    action,
				DryRun:    dryRun,
				Error:     syncErr,
			})

		case *corev1.Service:
			if res.Namespace != "" {
				targetNamespace = res.Namespace
			}
			action, syncErr := syncService(ctx, client, res, targetNamespace, dryRun)
			results = append(results, ResourceResult{
				Kind:      "Service",
				Name:      res.Name,
				Namespace: targetNamespace,
				Action:    action,
				DryRun:    dryRun,
				Error:     syncErr,
			})

		default:
			log.Printf("Resource type %q (doc #%d) is not currently supported; skipping", gvk.Kind, docIndex)
			results = append(results, ResourceResult{
				Kind:      gvk.Kind,
				Namespace: targetNamespace,
				Action:    ActionSkipped,
			})
		}
	}

	return results, nil
}

// syncDeployment reconciles a single Deployment resource.
func syncDeployment(
	ctx context.Context,
	client kubernetes.Interface,
	desired *appsv1.Deployment,
	namespace string,
	dryRun bool,
) (SyncAction, error) {
	existing, err := client.AppsV1().Deployments(namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if dryRun {
			log.Printf("[DRY-RUN] Would create Deployment %s/%s", namespace, desired.Name)
			return ActionCreated, nil
		}
		toCreate := desired.DeepCopy()
		toCreate.Namespace = namespace
		_, err := client.AppsV1().Deployments(namespace).Create(ctx, toCreate, metav1.CreateOptions{})
		if err != nil {
			return ActionCreated, fmt.Errorf("failed to create deployment: %w", err)
		}
		log.Printf("Successfully created Deployment %s/%s", namespace, desired.Name)
		return ActionCreated, nil
	} else if err != nil {
		return "", fmt.Errorf("failed to check existing deployment %s/%s: %w", namespace, desired.Name, err)
	}

	// Avoid unnecessary update if nothing changed
	if isDeploymentEqual(existing, desired) {
		log.Printf("Deployment %s/%s is unchanged. Skipping update.", namespace, desired.Name)
		return ActionUnchanged, nil
	}

	if dryRun {
		log.Printf("[DRY-RUN] Would update Deployment %s/%s", namespace, desired.Name)
		return ActionUpdated, nil
	}

	toUpdate := desired.DeepCopy()
	toUpdate.Namespace = namespace
	toUpdate.ResourceVersion = existing.ResourceVersion

	_, err = client.AppsV1().Deployments(namespace).Update(ctx, toUpdate, metav1.UpdateOptions{})
	if err != nil {
		return ActionUpdated, fmt.Errorf("failed to update deployment: %w", err)
	}
	log.Printf("Successfully updated Deployment %s/%s", namespace, desired.Name)
	return ActionUpdated, nil
}

// syncService reconciles a single Service resource.
func syncService(
	ctx context.Context,
	client kubernetes.Interface,
	desired *corev1.Service,
	namespace string,
	dryRun bool,
) (SyncAction, error) {
	existing, err := client.CoreV1().Services(namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if dryRun {
			log.Printf("[DRY-RUN] Would create Service %s/%s", namespace, desired.Name)
			return ActionCreated, nil
		}
		toCreate := desired.DeepCopy()
		toCreate.Namespace = namespace
		_, err := client.CoreV1().Services(namespace).Create(ctx, toCreate, metav1.CreateOptions{})
		if err != nil {
			return ActionCreated, fmt.Errorf("failed to create service: %w", err)
		}
		log.Printf("Successfully created Service %s/%s", namespace, desired.Name)
		return ActionCreated, nil
	} else if err != nil {
		return "", fmt.Errorf("failed to check existing service %s/%s: %w", namespace, desired.Name, err)
	}

	if isServiceEqual(existing, desired) {
		log.Printf("Service %s/%s is unchanged. Skipping update.", namespace, desired.Name)
		return ActionUnchanged, nil
	}

	if dryRun {
		log.Printf("[DRY-RUN] Would update Service %s/%s", namespace, desired.Name)
		return ActionUpdated, nil
	}

	toUpdate := desired.DeepCopy()
	toUpdate.Namespace = namespace
	toUpdate.ResourceVersion = existing.ResourceVersion

	// Retain existing ClusterIP assignments
	toUpdate.Spec.ClusterIP = existing.Spec.ClusterIP
	toUpdate.Spec.ClusterIPs = existing.Spec.ClusterIPs

	_, err = client.CoreV1().Services(namespace).Update(ctx, toUpdate, metav1.UpdateOptions{})
	if err != nil {
		return ActionUpdated, fmt.Errorf("failed to update service: %w", err)
	}
	log.Printf("Successfully updated Service %s/%s", namespace, desired.Name)
	return ActionUpdated, nil
}

// isDeploymentEqual checks if the existing deployment matches desired spec.
func isDeploymentEqual(existing, desired *appsv1.Deployment) bool {
	if desired.Spec.Replicas != nil {
		if existing.Spec.Replicas == nil || *existing.Spec.Replicas != *desired.Spec.Replicas {
			return false
		}
	}

	if len(existing.Spec.Template.Spec.Containers) != len(desired.Spec.Template.Spec.Containers) {
		return false
	}
	for i := range desired.Spec.Template.Spec.Containers {
		dCtr := desired.Spec.Template.Spec.Containers[i]
		eCtr := existing.Spec.Template.Spec.Containers[i]
		if dCtr.Name != eCtr.Name || dCtr.Image != eCtr.Image {
			return false
		}
		if !reflect.DeepEqual(dCtr.Command, eCtr.Command) || !reflect.DeepEqual(dCtr.Args, eCtr.Args) {
			return false
		}
		if !reflect.DeepEqual(dCtr.Env, eCtr.Env) {
			return false
		}
		if !reflect.DeepEqual(dCtr.Resources, eCtr.Resources) {
			return false
		}
		if !reflect.DeepEqual(dCtr.Ports, eCtr.Ports) {
			return false
		}
	}

	if desired.Spec.Selector != nil && existing.Spec.Selector != nil {
		if !reflect.DeepEqual(existing.Spec.Selector.MatchLabels, desired.Spec.Selector.MatchLabels) {
			return false
		}
	}

	for k, v := range desired.Labels {
		if existing.Labels[k] != v {
			return false
		}
	}

	return true
}

// isServiceEqual checks if the existing service matches desired spec.
func isServiceEqual(existing, desired *corev1.Service) bool {
	if desired.Spec.Type != "" && existing.Spec.Type != desired.Spec.Type {
		return false
	}

	if !reflect.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
		return false
	}

	if len(existing.Spec.Ports) != len(desired.Spec.Ports) {
		return false
	}
	for i := range desired.Spec.Ports {
		dP := desired.Spec.Ports[i]
		eP := existing.Spec.Ports[i]
		if dP.Port != eP.Port || dP.TargetPort != eP.TargetPort || dP.Protocol != eP.Protocol {
			return false
		}
	}

	for k, v := range desired.Labels {
		if existing.Labels[k] != v {
			return false
		}
	}

	return true
}

// PrintSyncSummary logs a clean table/summary of reconciliation actions.
func PrintSyncSummary(results []ResourceResult) {
	fmt.Println("--- Synchronization Summary ---")
	for _, r := range results {
		mode := "LIVE"
		if r.DryRun {
			mode = "DRY-RUN"
		}
		if r.Error != nil {
			fmt.Printf(" [FAILED]  [%s] %s %s/%s: %v\n", mode, r.Kind, r.Namespace, r.Name, r.Error)
		} else {
			fmt.Printf(" [%s] [%s] %s %s/%s\n", r.Action, mode, r.Kind, r.Namespace, r.Name)
		}
	}
	fmt.Println("-------------------------------")
}
