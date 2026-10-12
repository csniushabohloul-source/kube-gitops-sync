// Package main provides the CLI entry point for Akit-Ops Lite.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
)

func main() {
	log.Println("==================================================")
	log.Println("           Akit-Ops Lite - GitOps Tool            ")
	log.Println("==================================================")

	// Parse and validate configuration
	cfg, err := ParseConfig(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		log.Fatalf("Configuration error: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	log.Printf("Repository URL:     %s", cfg.RepoURL)
	log.Printf("Local Repo Path:    %s", cfg.RepoPath)
	log.Printf("Manifest File:      %s", cfg.ManifestFile)
	log.Printf("Default Namespace:  %s", cfg.Namespace)
	log.Printf("Poll Interval:      %v", cfg.PullInterval)
	log.Printf("Run Once Mode:      %t", cfg.Once)
	log.Printf("Dry-Run Mode:       %t", cfg.DryRun)

	// Graceful shutdown handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize Kubernetes client
	k8sClient, err := GetKubernetesClient(cfg.Kubeconfig)
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	// Initialize Git repository manager
	gitMgr := NewGitManager(cfg.RepoURL, cfg.RepoPath)
	if err := gitMgr.Initialize(); err != nil {
		log.Fatalf("Failed to initialize git repository: %v", err)
	}

	// Initial sync pass
	manifestPath := filepath.Join(cfg.RepoPath, cfg.ManifestFile)
	log.Printf("Performing initial synchronization using %s...", manifestPath)
	if err := syncOnce(ctx, k8sClient, manifestPath, cfg.Namespace, cfg.DryRun); err != nil {
		log.Printf("Initial synchronization completed with warnings/errors: %v", err)
	}

	if cfg.Once {
		log.Println("Run-once mode completed successfully. Exiting.")
		return
	}

	// Polling loop
	log.Printf("Watching for changes every %v. Press Ctrl+C to terminate.", cfg.PullInterval)
	ticker := time.NewTicker(cfg.PullInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Termination signal received. Shutting down gracefully...")
			return
		case <-ticker.C:
			changed, err := gitMgr.CheckForChanges()
			if err != nil {
				log.Printf("Git poll error: %v (will retry in %v)", err, cfg.PullInterval)
				continue
			}

			if changed {
				log.Println("New commits detected in repository. Synchronizing manifests...")
				if err := syncOnce(ctx, k8sClient, manifestPath, cfg.Namespace, cfg.DryRun); err != nil {
					log.Printf("Reconciliation error: %v", err)
				}
			} else {
				log.Printf("Repository is up to date (HEAD: %s). Next check in %v.", shortHash(gitMgr.CurrentCommit()), cfg.PullInterval)
			}
		}
	}
}

// syncOnce reads the manifest file and reconciles all Kubernetes resources.
func syncOnce(ctx context.Context, client kubernetes.Interface, manifestPath, defaultNamespace string, dryRun bool) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest %s: %w", manifestPath, err)
	}

	results, err := DecodeAndSyncManifests(ctx, client, data, defaultNamespace, dryRun)
	if err != nil {
		return err
	}

	PrintSyncSummary(results)
	return nil
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}
