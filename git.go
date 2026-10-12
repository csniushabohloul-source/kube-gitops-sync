package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// GitManager manages cloning and updating the target Git repository.
type GitManager struct {
	RepoURL  string
	RepoPath string
	repo     *git.Repository
	lastHash plumbing.Hash
}

// NewGitManager initializes a new GitManager instance.
func NewGitManager(repoURL, repoPath string) *GitManager {
	return &GitManager{
		RepoURL:  repoURL,
		RepoPath: repoPath,
	}
}

// Initialize opens an existing cloned repository or performs a fresh clone.
func (g *GitManager) Initialize() error {
	if _, err := os.Stat(g.RepoPath); err == nil {
		r, err := git.PlainOpen(g.RepoPath)
		if err == nil {
			g.repo = r
			head, err := r.Head()
			if err == nil {
				g.lastHash = head.Hash()
				log.Printf("Opened existing git repository at %s (HEAD: %s)", g.RepoPath, shortHash(g.lastHash.String()))
				return nil
			}
		}

		log.Printf("Directory %s is not a valid git repository, cleaning up for fresh clone...", g.RepoPath)
		if err := os.RemoveAll(g.RepoPath); err != nil {
			return fmt.Errorf("failed to clean directory %s: %w", g.RepoPath, err)
		}
	}

	log.Printf("Cloning repository %s into %s...", g.RepoURL, g.RepoPath)
	r, err := git.PlainClone(g.RepoPath, false, &git.CloneOptions{
		URL:               g.RepoURL,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
	})
	if err != nil {
		return fmt.Errorf("failed to clone repository %s: %w", g.RepoURL, err)
	}

	g.repo = r
	head, err := r.Head()
	if err != nil {
		return fmt.Errorf("failed to read HEAD of cloned repository: %w", err)
	}
	g.lastHash = head.Hash()
	log.Printf("Cloned repository successfully (HEAD: %s)", shortHash(g.lastHash.String()))
	return nil
}

// CheckForChanges pulls the latest commits from origin and checks if the HEAD has changed.
func (g *GitManager) CheckForChanges() (bool, error) {
	if g.repo == nil {
		return false, errors.New("git repository is not initialized")
	}

	w, err := g.repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("failed to obtain worktree: %w", err)
	}

	err = w.Pull(&git.PullOptions{RemoteName: "origin"})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return false, fmt.Errorf("git pull failed: %w", err)
	}

	head, err := g.repo.Head()
	if err != nil {
		return false, fmt.Errorf("failed to retrieve current HEAD: %w", err)
	}

	newHash := head.Hash()
	if newHash != g.lastHash {
		log.Printf("Git commit changed: %s -> %s", shortHash(g.lastHash.String()), shortHash(newHash.String()))
		g.lastHash = newHash
		return true, nil
	}

	return false, nil
}

// CurrentCommit returns the current HEAD hash as a string.
func (g *GitManager) CurrentCommit() string {
	if g.lastHash.IsZero() {
		return "unknown"
	}
	return g.lastHash.String()
}
