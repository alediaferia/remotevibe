// Package ghclient lists the repositories the configured token can see.
//
// remotevibe is a single-user tool: one fine-grained personal access token
// covers listing repos here, cloning inside the container, and pushing back.
// There is deliberately no OAuth device flow.
package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://api.github.com"

// Repo is the projection the phone UI needs.
type Repo struct {
	FullName      string    `json:"full_name"`
	Name          string    `json:"name"`
	Owner         string    `json:"owner"`
	Private       bool      `json:"private"`
	Description   string    `json:"description"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	Language      string    `json:"language"`
}

type apiRepo struct {
	FullName string `json:"full_name"`
	Name     string `json:"name"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	Private       bool      `json:"private"`
	Description   string    `json:"description"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	Language      string    `json:"language"`
	Archived      bool      `json:"archived"`
}

type Client struct {
	token string
	http  *http.Client

	mu       sync.Mutex
	cache    []Repo
	cachedAt time.Time
	ttl      time.Duration
}

func New(token string) *Client {
	return &Client{
		token: token,
		http:  &http.Client{Timeout: 20 * time.Second},
		ttl:   90 * time.Second,
	}
}

// List returns every non-archived repo the token can reach, most recently
// pushed first, optionally filtered by a case-insensitive substring.
func (c *Client) List(ctx context.Context, query string) ([]Repo, error) {
	repos, err := c.all(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return repos, nil
	}
	filtered := make([]Repo, 0, len(repos))
	for _, r := range repos {
		if strings.Contains(strings.ToLower(r.FullName), query) ||
			strings.Contains(strings.ToLower(r.Description), query) {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

// Get returns a single repo by "owner/name".
func (c *Client) Get(ctx context.Context, fullName string) (*Repo, error) {
	repos, err := c.all(ctx)
	if err != nil {
		return nil, err
	}
	for i := range repos {
		if strings.EqualFold(repos[i].FullName, fullName) {
			return &repos[i], nil
		}
	}
	return nil, fmt.Errorf("repository %q is not visible to this token", fullName)
}

// Invalidate drops the cached repo list (used after an explicit refresh).
func (c *Client) Invalidate() {
	c.mu.Lock()
	c.cachedAt = time.Time{}
	c.mu.Unlock()
}

func (c *Client) all(ctx context.Context) ([]Repo, error) {
	c.mu.Lock()
	if time.Since(c.cachedAt) < c.ttl && c.cache != nil {
		defer c.mu.Unlock()
		return c.cache, nil
	}
	c.mu.Unlock()

	var out []Repo
	for page := 1; page <= 10; page++ { // hard cap: 1000 repos
		batch, err := c.page(ctx, page)
		if err != nil {
			return nil, err
		}
		for _, r := range batch {
			if r.Archived {
				continue
			}
			out = append(out, Repo{
				FullName:      r.FullName,
				Name:          r.Name,
				Owner:         r.Owner.Login,
				Private:       r.Private,
				Description:   r.Description,
				DefaultBranch: r.DefaultBranch,
				PushedAt:      r.PushedAt,
				Language:      r.Language,
			})
		}
		if len(batch) < 100 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PushedAt.After(out[j].PushedAt) })

	c.mu.Lock()
	c.cache, c.cachedAt = out, time.Now()
	c.mu.Unlock()
	return out, nil
}

func (c *Client) page(ctx context.Context, page int) ([]apiRepo, error) {
	url := fmt.Sprintf("%s/user/repos?per_page=100&sort=pushed&page=%d&affiliation=owner,collaborator,organization_member", apiBase, page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("github rejected the token (%s) — check RV_GITHUB_TOKEN scopes", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s", resp.Status)
	}
	var repos []apiRepo
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, fmt.Errorf("github: decode: %w", err)
	}
	return repos, nil
}
