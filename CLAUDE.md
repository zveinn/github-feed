# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GitAI is a Go CLI tool for monitoring GitHub pull requests and issues across repositories. It tracks contributions, reviews, and assignments with colorized output and real-time progress visualization.

## Build & Run

```bash
# Build the binary
go build -o gitai main.go

# Run directly
./gitai

# Run with flags
./gitai --closed        # Include closed/merged items from last month
./gitai --debug         # Show detailed API logging instead of progress bar
```

## Configuration

The tool requires a GitHub Personal Access Token with `repo` and `read:org` scopes. Configuration is loaded from:
1. Environment variables: `GITHUB_TOKEN` or `GITHUB_ACTIVITY_TOKEN`, and `GITHUB_USERNAME` or `GITHUB_USER`
2. Config file at `~/.secret/.gitai.env` (see main.go:179)

## Architecture & Key Components

### Data Flow
1. **Parallel API Fetching** (main.go:345-380): Seven concurrent GitHub search queries for PRs (authored, mentioned, assigned, commented, reviewed, review-requested, involved) plus event polling
2. **Issue Collection** (main.go:395-417): Five parallel searches for issues with similar categories
3. **Cross-Reference Detection** (main.go:419-472): Links issues to PRs by checking PR/issue bodies and comments for references
4. **Display Rendering** (main.go:531-601): Separates items into sections by state (open/merged/closed) with colorized output

### Core Data Structures

**PRActivity** (main.go:20-27): Represents a PR with metadata:
- Label: How the user is involved (e.g., "Authored", "Reviewed")
- Owner/Repo: Repository identification
- PR: GitHub PullRequest object
- Issues: Linked issues that reference this PR

**IssueActivity** (main.go:30-36): Represents an issue with similar metadata structure

**Progress** (main.go:39-91): Thread-safe progress tracking with colored bar display. Used throughout API calls to show real-time fetch status.

### Key Functions

**fetchAndDisplayActivity** (main.go:288-602): Main orchestration function that:
- Checks GitHub API rate limits
- Launches parallel PR/issue searches
- Performs cross-reference detection
- Sorts and displays results by state

**areCrossReferenced** (main.go:604-646): Determines if a PR and issue reference each other by:
- Checking PR/issue body text for mentions
- Fetching and checking PR comments for issue references
- Uses mentionsNumber() to detect patterns like "#123", "fixes #123", or full GitHub URLs

**collectSearchResults** (main.go:808-911): Generic function for PR search that:
- Paginates through GitHub search results
- Deduplicates using seenPRs map
- Updates progress tracker after each API call

**Color System** (main.go:93-149): Consistent color coding:
- getUserColor(): Hash-based consistent colors per username
- getLabelColor(): Fixed colors for involvement types
- getStateColor(): Fixed colors for PR/issue states

### Concurrency Patterns

The codebase uses `sync.WaitGroup` with goroutines (via `.Go()` method) for parallel API calls. All concurrent access to shared maps (seenPRs, seenIssues) is protected by mutex locks (main.go:304, 389).

Progress tracking is thread-safe (main.go:45-55) and updated after each API call across all goroutines.

### Date Filtering

Two modes controlled by `--closed` flag (main.go:323-332):
- Default: Open items updated in last 6 months
- Closed mode: All items (open/closed) updated in last month

## GitHub API Integration

Uses `google/go-github/v57` library. Key API patterns:
- Search API for bulk queries (main.go:821): `client.Search.Issues()`
- Events API for recent activity (main.go:707): `client.Activity.ListEventsPerformedByUser()`
- PullRequests API for details (main.go:866): `client.PullRequests.Get()`
- Rate limit checking (main.go:246-286): Monitors both core (5000/hr) and search (30/min) limits

## Testing Considerations

When modifying this codebase:
- Test with both `--debug` and default modes (progress bar vs. detailed logs)
- Verify mutex protection on any new shared data structures
- Test cross-reference detection with various mention patterns (see mentionsNumber main.go:651-691)
- Consider GitHub API rate limits when adding new API calls
