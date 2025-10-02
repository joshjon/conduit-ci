package github

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RepoRef struct {
	// Repo is "owner/repo" (required).
	Repo string
	// Ref can be a branch, tag, or (short) SHA (required).
	Ref string
	// Subdir, if set, extracts only this path within the repo.
	Subdir string
	// Token (optional) is a GitHub token used to raise rate limits.
	Token string
}

// Fetch resolves f.Ref to a pinned commit SHA, downloads the tarball of that
// commit, and extracts it into dest. If Subdir is set, only that subfolder is
// extracted (re-rooted at dest). Returns the pinned commit SHA.
func (f *RepoRef) Fetch(ctx context.Context, dest string) (string, error) {
	if f.Repo == "" {
		return "", fmt.Errorf("repo is required (owner/repo)")
	}
	if f.Ref == "" {
		return "", fmt.Errorf("ref is required")
	}
	owner, name, err := splitOwnerRepo(f.Repo)
	if err != nil {
		return "", err
	}
	c := &http.Client{Timeout: 60 * time.Second}

	sha, err := resolveCommitSHA(ctx, c, owner, name, f.Ref, f.Token)
	if err != nil {
		return "", fmt.Errorf("resolve commit: %w", err)
	}

	rc, err := downloadTarball(ctx, c, owner, name, sha, f.Token)
	if err != nil {
		return "", fmt.Errorf("download tarball: %w", err)
	}
	defer rc.Close()

	if err := extractTarGz(rc, dest, f.Subdir); err != nil {
		return "", fmt.Errorf("extract tarball: %w", err)
	}
	return sha, nil
}

type commitResp struct {
	SHA string `json:"sha"`
}

func splitOwnerRepo(repo string) (string, string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("repo must be in 'owner/repo' form")
	}
	return parts[0], parts[1], nil
}

func resolveCommitSHA(ctx context.Context, c *http.Client, owner, repo, ref, token string) (string, error) {
	url := "https://api.github.com/repos/" + owner + "/" + repo + "/commits/" + ref
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	addAuthHeaders(req, token)

	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("ref %q not found", ref)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return "", fmt.Errorf("commit resolve failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var cr commitResp
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", err
	}
	if cr.SHA == "" {
		return "", errors.New("empty SHA in response")
	}
	return cr.SHA, nil
}

func downloadTarball(ctx context.Context, c *http.Client, owner, repo, sha, token string) (io.ReadCloser, error) {
	url := "https://api.github.com/repos/" + owner + "/" + repo + "/tarball/" + sha
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	addAuthHeaders(req, token)

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("tarball download failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return resp.Body, nil
}

func addAuthHeaders(req *http.Request, token string) {
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gh-pin-fetch/1.1")
}

func extractTarGz(r io.Reader, dest, subdir string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var topLevel string
	trimPrefix := func(name string) (string, bool) {
		name = filepath.ToSlash(name)
		if topLevel == "" {
			i := strings.IndexRune(name, '/')
			if i <= 0 {
				return "", false
			}
			topLevel = name[:i+1]
		}
		if !strings.HasPrefix(name, topLevel) {
			return "", false
		}
		return strings.TrimPrefix(name, topLevel), true
	}

	sub := strings.Trim(strings.TrimPrefix(filepath.ToSlash(subdir), "./"), "/")
	onlySubdir := sub != ""

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}

		rel, ok := trimPrefix(hdr.Name)
		if !ok || rel == "" {
			continue
		}

		if onlySubdir {
			if !(rel == sub || strings.HasPrefix(rel, sub+"/")) {
				continue
			}
			rel = strings.TrimPrefix(rel, sub)
			rel = strings.TrimPrefix(rel, "/")
			if rel == "" && hdr.FileInfo().IsDir() {
				continue
			}
		}

		target := filepath.Join(dest, filepath.FromSlash(rel))

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o666)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			_ = f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Symlink(hdr.Linkname, target)
		default:
			continue
		}
	}
	return nil
}
