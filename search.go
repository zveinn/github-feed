package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

// GitHubSearchResponse represents the GitHub Search API response
type GitHubSearchResponse struct {
	TotalCount        int                `json:"total_count"`
	IncompleteResults bool               `json:"incomplete_results"`
	Items             []GitHubSearchItem `json:"items"`
}

// GitHubSearchItem represents a single search result (repo or issue)
type GitHubSearchItem struct {
	ID              int64       `json:"id"`
	NodeID          string      `json:"node_id"`
	Name            string      `json:"name,omitempty"`      // For repositories
	FullName        string      `json:"full_name,omitempty"` // For repositories
	Owner           *GitHubUser `json:"owner,omitempty"`
	HTMLURL         string      `json:"html_url"`
	Description     string      `json:"description,omitempty"`
	CreatedAt       string      `json:"created_at"`
	UpdatedAt       string      `json:"updated_at"`
	PushedAt        string      `json:"pushed_at,omitempty"`         // For repositories
	Size            int         `json:"size,omitempty"`              // For repositories
	StargazersCount int         `json:"stargazers_count,omitempty"`  // For repositories
	Language        string      `json:"language,omitempty"`          // For repositories
	ForksCount      int         `json:"forks_count,omitempty"`       // For repositories
	OpenIssuesCount int         `json:"open_issues_count,omitempty"` // For repositories
	DefaultBranch   string      `json:"default_branch,omitempty"`    // For repositories

	// For issues/PRs
	Number        int                `json:"number,omitempty"`
	Title         string             `json:"title,omitempty"`
	User          *GitHubUser        `json:"user,omitempty"`
	State         string             `json:"state,omitempty"`
	Locked        bool               `json:"locked,omitempty"`
	Comments      int                `json:"comments,omitempty"`
	ClosedAt      *string            `json:"closed_at,omitempty"`
	Body          string             `json:"body,omitempty"`
	PullRequest   *GitHubPRReference `json:"pull_request,omitempty"` // Present if item is a PR
	RepositoryURL string             `json:"repository_url,omitempty"`
}

// GitHubUser represents a GitHub user
type GitHubUser struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	NodeID    string `json:"node_id"`
	AvatarURL string `json:"avatar_url"`
	HTMLURL   string `json:"html_url"`
	Type      string `json:"type"`
}

// GitHubPRReference indicates if an issue is actually a PR
type GitHubPRReference struct {
	URL      string `json:"url"`
	HTMLURL  string `json:"html_url"`
	DiffURL  string `json:"diff_url"`
	PatchURL string `json:"patch_url"`
}

// SearchReposAndIssues searches for issues and pull requests in a single query
// The query parameter should be a GitHub search query string
// To search both issues and PRs, don't specify a type in the query
// The page parameter specifies which page of results to retrieve (1-indexed, 0 means first page)
// Returns the search response or an error
func SearchReposAndIssues(query string, page int) (*GitHubSearchResponse, error) {
	token := os.Getenv("GITHUB_ACTIVITY_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN or GITHUB_ACTIVITY_TOKEN environment variable is required")
	}

	// Use the issues endpoint which can search both issues and PRs
	// Note: GitHub Search API has different endpoints for repos vs issues
	// The issues endpoint can return both issues and PRs
	// To get both: omit the "type:" qualifier or use "is:issue,pr"
	baseURL := "https://api.github.com/search/issues"

	// Example queries:
	// - Both Issues and PRs: "involves:username" (no type specified)
	// - Issues only: "involves:username type:issue"
	// - PRs only: "involves:username type:pr"

	// Encode the query parameter
	params := url.Values{}
	params.Add("q", query)
	params.Add("per_page", "100") // Max results per page

	// Add pagination - GitHub API uses 1-indexed pages
	if page > 0 {
		params.Add("page", fmt.Sprintf("%d", page))
	}

	// Construct full URL
	searchURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	// Create HTTP request
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	// Make the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read and parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var searchResponse GitHubSearchResponse
	if err := json.Unmarshal(body, &searchResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &searchResponse, nil
}

// SearchIssuesAndPRs searches for issues and pull requests using the issues search endpoint
// The query parameter should be a GitHub search query string
// The page parameter specifies which page of results to retrieve (1-indexed, 0 means first page)
// Example: "involves:username type:issue" or "involves:username type:pr"
func SearchIssuesAndPRs(query string, page int) (*GitHubSearchResponse, error) {
	token := os.Getenv("GITHUB_ACTIVITY_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN or GITHUB_ACTIVITY_TOKEN environment variable is required")
	}

	// Use the issues search endpoint (works for both issues and PRs)
	baseURL := "https://api.github.com/search/issues"

	// Encode the query parameter
	params := url.Values{}
	params.Add("q", query)
	params.Add("per_page", "100") // Max results per page
	params.Add("sort", "updated")
	params.Add("order", "desc")

	// Add pagination - GitHub API uses 1-indexed pages
	if page > 0 {
		params.Add("page", fmt.Sprintf("%d", page))
	}

	// Construct full URL
	searchURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	// Create HTTP request
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	// Make the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read and parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var searchResponse GitHubSearchResponse
	if err := json.Unmarshal(body, &searchResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &searchResponse, nil
}

// SearchCombined searches for both repositories and issues/PRs by making multiple API calls
// This is necessary because GitHub has separate endpoints for repos vs issues
// Returns a map with "repos", "issues", and "prs" keys (only fetches first page)
func SearchCombined(username string) (map[string]*GitHubSearchResponse, error) {
	results := make(map[string]*GitHubSearchResponse)

	// Search for repositories (first page only)
	repos, err := SearchReposAndIssues(fmt.Sprintf("user:%s", username), 1)
	if err != nil {
		return nil, fmt.Errorf("failed to search repos: %w", err)
	}
	results["repos"] = repos

	// Search for issues (first page only)
	issues, err := SearchIssuesAndPRs(fmt.Sprintf("involves:%s type:issue", username), 1)
	if err != nil {
		return nil, fmt.Errorf("failed to search issues: %w", err)
	}
	results["issues"] = issues

	// Search for PRs (first page only)
	prs, err := SearchIssuesAndPRs(fmt.Sprintf("involves:%s type:pr", username), 1)
	if err != nil {
		return nil, fmt.Errorf("failed to search PRs: %w", err)
	}
	results["prs"] = prs

	return results, nil
}

// SearchReposAndIssuesAllPages fetches all pages for a given query
// Returns all items combined from all pages
func SearchReposAndIssuesAllPages(query string, maxPages int) ([]GitHubSearchItem, error) {
	var allItems []GitHubSearchItem

	for page := 1; page <= maxPages; page++ {
		resp, err := SearchReposAndIssues(query, page)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch page %d: %w", page, err)
		}

		allItems = append(allItems, resp.Items...)

		// If we got fewer items than per_page, we've reached the last page
		if len(resp.Items) < 100 {
			break
		}

		// Also check if we've collected all items based on total_count
		if len(allItems) >= resp.TotalCount {
			break
		}
	}

	return allItems, nil
}

// SearchIssuesAndPRsAllPages fetches all pages for a given query
// Returns all items combined from all pages
func SearchIssuesAndPRsAllPages(query string, maxPages int) ([]GitHubSearchItem, error) {
	var allItems []GitHubSearchItem

	for page := 1; page <= maxPages; page++ {
		resp, err := SearchIssuesAndPRs(query, page)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch page %d: %w", page, err)
		}

		allItems = append(allItems, resp.Items...)

		// If we got fewer items than per_page, we've reached the last page
		if len(resp.Items) < 100 {
			break
		}

		// Also check if we've collected all items based on total_count
		if len(allItems) >= resp.TotalCount {
			break
		}
	}

	return allItems, nil
}
