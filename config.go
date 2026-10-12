package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config encapsulates all configuration options for Akit-Ops Lite.
type Config struct {
	RepoURL      string        // Remote or local Git repository URL
	RepoPath     string        // Local filesystem directory where the repo is cloned
	PullInterval time.Duration // Duration between git synchronization checks
	ManifestFile string        // Relative path to the Kubernetes manifest inside the repo
	Namespace    string        // Default fallback namespace when not defined in the manifest
	Kubeconfig   string        // Path to kubeconfig file (for out-of-cluster execution)
	Once         bool          // Perform a single synchronization and exit
	DryRun       bool          // Simulate operations without modifying Kubernetes resources
}

// DefaultConfig returns a Config with sensible default values.
func DefaultConfig() *Config {
	home, err := os.UserHomeDir()
	defaultKubeconfig := ""
	if err == nil && home != "" {
		defaultKubeconfig = filepath.Join(home, ".kube", "config")
	}

	return &Config{
		RepoURL:      "",
		RepoPath:     "./deployrepo",
		PullInterval: 30 * time.Second,
		ManifestFile: "manifest.yaml",
		Namespace:    "default",
		Kubeconfig:   defaultKubeconfig,
		Once:         false,
		DryRun:       false,
	}
}

// ParseConfig parses configuration from CLI arguments and environment variables.
// CLI flags take precedence over environment variables.
func ParseConfig(args []string) (*Config, error) {
	cfg := DefaultConfig()

	fs := flag.NewFlagSet("akit-ops-lite", flag.ContinueOnError)

	var (
		flagRepoURL    = fs.String("repo-url", "", "Git repository URL to clone and sync (env: REPO_URL)")
		flagRepoPath   = fs.String("repo-path", "", "Local path to clone the repository (env: REPO_PATH)")
		flagInterval   = fs.String("interval", "", "Polling interval, e.g. 15s or 30 (env: REPO_PULL_INTERVAL)")
		flagManifest   = fs.String("manifest", "", "Manifest filename in repository (env: MANIFEST_FILE)")
		flagNamespace  = fs.String("namespace", "", "Default fallback Kubernetes namespace (env: K8S_NAMESPACE)")
		flagKubeconfig = fs.String("kubeconfig", "", "Path to kubeconfig file (env: KUBECONFIG)")
		flagOnce       = fs.Bool("once", false, "Run synchronization once and exit (env: SYNC_ONCE)")
		flagDryRun     = fs.Bool("dry-run", false, "Simulate changes without modifying Kubernetes resources (env: DRY_RUN)")
	)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to parse command-line arguments: %w", err)
	}

	if *flagRepoURL != "" {
		cfg.RepoURL = *flagRepoURL
	} else if env := os.Getenv("REPO_URL"); env != "" {
		cfg.RepoURL = env
	}

	if *flagRepoPath != "" {
		cfg.RepoPath = *flagRepoPath
	} else if env := os.Getenv("REPO_PATH"); env != "" {
		cfg.RepoPath = env
	}

	if *flagManifest != "" {
		cfg.ManifestFile = *flagManifest
	} else if env := os.Getenv("MANIFEST_FILE"); env != "" {
		cfg.ManifestFile = env
	}

	if *flagNamespace != "" {
		cfg.Namespace = *flagNamespace
	} else if env := os.Getenv("K8S_NAMESPACE"); env != "" {
		cfg.Namespace = env
	}

	if *flagKubeconfig != "" {
		cfg.Kubeconfig = *flagKubeconfig
	} else if env := os.Getenv("KUBECONFIG"); env != "" {
		cfg.Kubeconfig = env
	}

	if *flagOnce {
		cfg.Once = true
	} else if env := os.Getenv("SYNC_ONCE"); env != "" {
		val, err := strconv.ParseBool(env)
		if err == nil {
			cfg.Once = val
		}
	}

	if *flagDryRun {
		cfg.DryRun = true
	} else if env := os.Getenv("DRY_RUN"); env != "" {
		val, err := strconv.ParseBool(env)
		if err == nil {
			cfg.DryRun = val
		}
	}

	rawInterval := ""
	if *flagInterval != "" {
		rawInterval = *flagInterval
	} else if env := os.Getenv("REPO_PULL_INTERVAL"); env != "" {
		rawInterval = env
	}

	if rawInterval != "" {
		// Try parsing as duration first (e.g., "15s", "1m")
		dur, err := time.ParseDuration(rawInterval)
		if err == nil {
			cfg.PullInterval = dur
		} else {
			// Try parsing as integer seconds (e.g., "15")
			sec, err := strconv.Atoi(rawInterval)
			if err != nil {
				return nil, fmt.Errorf("invalid pull interval %q: must be a duration (e.g. 30s) or seconds integer (e.g. 30)", rawInterval)
			}
			cfg.PullInterval = time.Duration(sec) * time.Second
		}
	}

	return cfg, nil
}

// Validate checks whether the configuration is valid and safe to run.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.RepoURL) == "" {
		return fmt.Errorf("repository URL is required (use --repo-url or REPO_URL)")
	}

	if err := validateRepoURL(c.RepoURL); err != nil {
		return fmt.Errorf("invalid repository URL %q: %w", c.RepoURL, err)
	}

	if strings.TrimSpace(c.ManifestFile) == "" {
		return fmt.Errorf("manifest filename cannot be empty")
	}

	if strings.TrimSpace(c.Namespace) == "" {
		return fmt.Errorf("default namespace cannot be empty")
	}

	if strings.TrimSpace(c.RepoPath) == "" {
		return fmt.Errorf("repo path cannot be empty")
	}

	// If not running in once mode, pull interval must be positive
	if !c.Once && c.PullInterval <= 0 {
		return fmt.Errorf("pull interval must be greater than zero, got %v", c.PullInterval)
	}

	return nil
}

// validateRepoURL validates that the git repository URL format is acceptable.
func validateRepoURL(raw string) error {
	trimmed := strings.TrimSpace(raw)

	// Support local directory paths for testing
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "./") ||
		strings.HasPrefix(trimmed, "../") || strings.HasPrefix(trimmed, "file://") ||
		(len(trimmed) > 2 && trimmed[1] == ':' && (trimmed[2] == '\\' || trimmed[2] == '/')) {
		return nil
	}

	// Support SSH-style git URLs (e.g., git@github.com:user/repo.git)
	if strings.HasPrefix(trimmed, "git@") || strings.HasPrefix(trimmed, "ssh://") {
		return nil
	}

	// Support HTTP / HTTPS URLs
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("unable to parse URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q, must be http, https, git@, ssh:// or a local path", parsed.Scheme)
	}

	if parsed.Host == "" {
		return fmt.Errorf("URL missing host")
	}

	return nil
}
