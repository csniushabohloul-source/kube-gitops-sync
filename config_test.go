package main

import (
	"os"
	"testing"
	"time"
)

func TestParseConfigFlags(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantURL      string
		wantPath     string
		wantInterval time.Duration
		wantOnce     bool
		wantDryRun   bool
		wantNS       string
		wantErr      bool
	}{
		{
			name:         "All flags provided",
			args:         []string{"--repo-url", "https://github.com/example/k8s-repo.git", "--repo-path", "/tmp/repo", "--interval", "10s", "--once", "--dry-run", "--namespace", "staging"},
			wantURL:      "https://github.com/example/k8s-repo.git",
			wantPath:     "/tmp/repo",
			wantInterval: 10 * time.Second,
			wantOnce:     true,
			wantDryRun:   true,
			wantNS:       "staging",
			wantErr:      false,
		},
		{
			name:         "Integer seconds interval",
			args:         []string{"--repo-url", "https://github.com/example/k8s-repo.git", "--interval", "45"},
			wantURL:      "https://github.com/example/k8s-repo.git",
			wantPath:     "./deployrepo",
			wantInterval: 45 * time.Second,
			wantOnce:     false,
			wantDryRun:   false,
			wantNS:       "default",
			wantErr:      false,
		},
		{
			name:    "Invalid interval format",
			args:    []string{"--repo-url", "https://github.com/example/k8s-repo.git", "--interval", "not-a-number"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseConfig(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if cfg.RepoURL != tt.wantURL {
				t.Errorf("RepoURL = %v, want %v", cfg.RepoURL, tt.wantURL)
			}
			if cfg.RepoPath != tt.wantPath {
				t.Errorf("RepoPath = %v, want %v", cfg.RepoPath, tt.wantPath)
			}
			if cfg.PullInterval != tt.wantInterval {
				t.Errorf("PullInterval = %v, want %v", cfg.PullInterval, tt.wantInterval)
			}
			if cfg.Once != tt.wantOnce {
				t.Errorf("Once = %v, want %v", cfg.Once, tt.wantOnce)
			}
			if cfg.DryRun != tt.wantDryRun {
				t.Errorf("DryRun = %v, want %v", cfg.DryRun, tt.wantDryRun)
			}
			if cfg.Namespace != tt.wantNS {
				t.Errorf("Namespace = %v, want %v", cfg.Namespace, tt.wantNS)
			}
		})
	}
}

func TestParseConfigEnvVars(t *testing.T) {
	// Set environment variables
	os.Setenv("REPO_URL", "https://github.com/env/repo.git")
	os.Setenv("REPO_PULL_INTERVAL", "20")
	os.Setenv("REPO_PATH", "/env/path")
	os.Setenv("MANIFEST_FILE", "custom.yaml")
	os.Setenv("K8S_NAMESPACE", "custom-ns")
	os.Setenv("SYNC_ONCE", "true")
	os.Setenv("DRY_RUN", "true")

	defer func() {
		os.Unsetenv("REPO_URL")
		os.Unsetenv("REPO_PULL_INTERVAL")
		os.Unsetenv("REPO_PATH")
		os.Unsetenv("MANIFEST_FILE")
		os.Unsetenv("K8S_NAMESPACE")
		os.Unsetenv("SYNC_ONCE")
		os.Unsetenv("DRY_RUN")
	}()

	cfg, err := ParseConfig([]string{})
	if err != nil {
		t.Fatalf("ParseConfig() unexpected error: %v", err)
	}

	if cfg.RepoURL != "https://github.com/env/repo.git" {
		t.Errorf("expected RepoURL from env, got %s", cfg.RepoURL)
	}
	if cfg.PullInterval != 20*time.Second {
		t.Errorf("expected PullInterval 20s, got %v", cfg.PullInterval)
	}
	if cfg.RepoPath != "/env/path" {
		t.Errorf("expected RepoPath /env/path, got %s", cfg.RepoPath)
	}
	if cfg.ManifestFile != "custom.yaml" {
		t.Errorf("expected ManifestFile custom.yaml, got %s", cfg.ManifestFile)
	}
	if cfg.Namespace != "custom-ns" {
		t.Errorf("expected Namespace custom-ns, got %s", cfg.Namespace)
	}
	if !cfg.Once {
		t.Errorf("expected Once to be true from env")
	}
	if !cfg.DryRun {
		t.Errorf("expected DryRun to be true from env")
	}

	// Verify CLI flag takes precedence over env var
	cfgOverride, err := ParseConfig([]string{"--repo-url", "https://github.com/flag/repo.git", "--interval", "5s"})
	if err != nil {
		t.Fatalf("ParseConfig() override error: %v", err)
	}
	if cfgOverride.RepoURL != "https://github.com/flag/repo.git" {
		t.Errorf("expected CLI flag override for RepoURL, got %s", cfgOverride.RepoURL)
	}
	if cfgOverride.PullInterval != 5*time.Second {
		t.Errorf("expected CLI flag override for PullInterval, got %v", cfgOverride.PullInterval)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "Valid HTTPS URL",
			config: Config{
				RepoURL:      "https://github.com/example/repo.git",
				RepoPath:     "./deployrepo",
				PullInterval: 15 * time.Second,
				ManifestFile: "manifest.yaml",
				Namespace:    "default",
			},
			wantErr: false,
		},
		{
			name: "Valid SSH URL",
			config: Config{
				RepoURL:      "git@github.com:example/repo.git",
				RepoPath:     "./deployrepo",
				PullInterval: 15 * time.Second,
				ManifestFile: "manifest.yaml",
				Namespace:    "default",
			},
			wantErr: false,
		},
		{
			name: "Valid local relative path URL",
			config: Config{
				RepoURL:      "./local-repo",
				RepoPath:     "./deployrepo",
				PullInterval: 10 * time.Second,
				ManifestFile: "manifest.yaml",
				Namespace:    "default",
			},
			wantErr: false,
		},
		{
			name: "Missing RepoURL",
			config: Config{
				RepoURL:      "",
				RepoPath:     "./deployrepo",
				PullInterval: 15 * time.Second,
				ManifestFile: "manifest.yaml",
				Namespace:    "default",
			},
			wantErr: true,
		},
		{
			name: "Invalid URL scheme",
			config: Config{
				RepoURL:      "ftp://example.com/repo.git",
				RepoPath:     "./deployrepo",
				PullInterval: 15 * time.Second,
				ManifestFile: "manifest.yaml",
				Namespace:    "default",
			},
			wantErr: true,
		},
		{
			name: "Zero pull interval without once mode",
			config: Config{
				RepoURL:      "https://github.com/example/repo.git",
				RepoPath:     "./deployrepo",
				PullInterval: 0,
				Once:         false,
				ManifestFile: "manifest.yaml",
				Namespace:    "default",
			},
			wantErr: true,
		},
		{
			name: "Negative pull interval without once mode",
			config: Config{
				RepoURL:      "https://github.com/example/repo.git",
				RepoPath:     "./deployrepo",
				PullInterval: -5 * time.Second,
				Once:         false,
				ManifestFile: "manifest.yaml",
				Namespace:    "default",
			},
			wantErr: true,
		},
		{
			name: "Zero pull interval with once mode is allowed",
			config: Config{
				RepoURL:      "https://github.com/example/repo.git",
				RepoPath:     "./deployrepo",
				PullInterval: 0,
				Once:         true,
				ManifestFile: "manifest.yaml",
				Namespace:    "default",
			},
			wantErr: false,
		},
		{
			name: "Empty manifest filename",
			config: Config{
				RepoURL:      "https://github.com/example/repo.git",
				RepoPath:     "./deployrepo",
				PullInterval: 15 * time.Second,
				ManifestFile: "",
				Namespace:    "default",
			},
			wantErr: true,
		},
		{
			name: "Empty namespace",
			config: Config{
				RepoURL:      "https://github.com/example/repo.git",
				RepoPath:     "./deployrepo",
				PullInterval: 15 * time.Second,
				ManifestFile: "manifest.yaml",
				Namespace:    "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
