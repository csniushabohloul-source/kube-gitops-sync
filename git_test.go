package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGitManager_InitializeExistingAndCorrupt(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "akit-ops-git-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	repoPath := filepath.Join(tempDir, "repo")

	// Initialize a valid git repo
	r, err := git.PlainInit(repoPath, false)
	if err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Create an initial commit
	w, err := r.Worktree()
	if err != nil {
		t.Fatalf("failed to get worktree: %v", err)
	}

	testFile := filepath.Join(repoPath, "manifest.yaml")
	if err := os.WriteFile(testFile, []byte("test: true\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err = w.Add("manifest.yaml")
	if err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	commit, err := w.Commit("Initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test Author",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("failed to create commit: %v", err)
	}

	// Initialize GitManager pointing to existing repository
	mgr := NewGitManager("", repoPath)
	if err := mgr.Initialize(); err != nil {
		t.Fatalf("GitManager.Initialize() failed on existing repo: %v", err)
	}

	if mgr.CurrentCommit() != commit.String() {
		t.Errorf("expected commit %s, got %s", commit.String(), mgr.CurrentCommit())
	}

	// Uninitialized manager check
	uninitMgr := NewGitManager("", "")
	_, err = uninitMgr.CheckForChanges()
	if err == nil {
		t.Error("expected error checking changes on uninitialized manager, got nil")
	}
}

func TestGitManager_CloneAndPull(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "akit-ops-git-clone-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	originPath := filepath.Join(tempDir, "origin")
	clonePath := filepath.Join(tempDir, "clone")

	// Create origin repository with initial commit
	rOrigin, err := git.PlainInit(originPath, false)
	if err != nil {
		t.Fatalf("failed to init origin repo: %v", err)
	}
	wOrigin, err := rOrigin.Worktree()
	if err != nil {
		t.Fatalf("failed to get origin worktree: %v", err)
	}
	testFile := filepath.Join(originPath, "manifest.yaml")
	if err := os.WriteFile(testFile, []byte("version: 1\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	if _, err := wOrigin.Add("manifest.yaml"); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}
	_, err = wOrigin.Commit("Commit 1", &git.CommitOptions{
		Author: &object.Signature{Name: "Tester", Email: "test@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Clone using GitManager
	mgr := NewGitManager(originPath, clonePath)
	if err := mgr.Initialize(); err != nil {
		t.Fatalf("failed to clone origin: %v", err)
	}

	// Verify no changes initially
	changed, err := mgr.CheckForChanges()
	if err != nil {
		t.Fatalf("CheckForChanges error: %v", err)
	}
	if changed {
		t.Errorf("expected no changes, got true")
	}

	// Create a new commit on origin
	if err := os.WriteFile(testFile, []byte("version: 2\n"), 0644); err != nil {
		t.Fatalf("failed to update test file: %v", err)
	}
	if _, err := wOrigin.Add("manifest.yaml"); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}
	commit2, err := wOrigin.Commit("Commit 2", &git.CommitOptions{
		Author: &object.Signature{Name: "Tester", Email: "test@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Pull changes and assert commit matches
	changed, err = mgr.CheckForChanges()
	if err != nil {
		t.Fatalf("CheckForChanges error after commit: %v", err)
	}
	if !changed {
		t.Errorf("expected change to be detected, got false")
	}
	if mgr.CurrentCommit() != commit2.String() {
		t.Errorf("expected current commit to be %s, got %s", commit2.String(), mgr.CurrentCommit())
	}
}
