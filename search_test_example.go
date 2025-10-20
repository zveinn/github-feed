package main

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// Example usage of the search functions
// This can be run as: go run search.go search_test_example.go
func exampleSearchUsage() {
	username := os.Getenv("GITHUB_USERNAME")
	if username == "" {
		username = os.Getenv("GITHUB_USER")
	}
	if username == "" {
		fmt.Println("GITHUB_USERNAME or GITHUB_USER environment variable is required")
		return
	}

	fmt.Printf("Searching for issues and PRs for user: %s\n\n", username)

	// Collect all results from all pages
	var allItems []GitHubSearchItem
	page := 1
	totalCount := 0

	for {
		results, err := SearchReposAndIssues(fmt.Sprintf("involves:%s", username), page)
		if err != nil {
			fmt.Printf("Error searching page %d: %v\n", page, err)
			break
		}

		totalCount = results.TotalCount
		allItems = append(allItems, results.Items...)
		fmt.Printf("Fetched page %d with %d items (total collected: %d/%d)\n", page, len(results.Items), len(allItems), totalCount)

		// Break if we got fewer items than per_page (last page)
		if len(results.Items) < 100 {
			break
		}

		page++
	}

	fmt.Printf("\nCollected %d total items across %d pages\n", len(allItems), page)

	// Sort by update date (most recent first)
	sort.Slice(allItems, func(i, j int) bool {
		timeI, errI := time.Parse(time.RFC3339, allItems[i].UpdatedAt)
		timeJ, errJ := time.Parse(time.RFC3339, allItems[j].UpdatedAt)

		// If parsing fails, put items with errors at the end
		if errI != nil {
			return false
		}
		if errJ != nil {
			return true
		}

		return timeI.After(timeJ) // Most recent first
	})

	fmt.Println("\nResults sorted by update date (most recent first):")

	// Display sorted results
	for _, item := range allItems {
		itemType := "Issue"
		if item.PullRequest != nil {
			itemType = "PR"
		}
		fmt.Printf("  - [%s] %s#%d: %s (updated: %s)\n", itemType, item.RepositoryURL, item.Number, item.Title, item.UpdatedAt)
	}
}

// Uncomment the main function below to run this example:
