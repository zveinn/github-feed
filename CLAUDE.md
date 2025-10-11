# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GitHub Feed is a Go CLI tool for monitoring GitHub pull requests and issues across repositories. It tracks contributions, reviews, and assignments with colorized output and real-time progress visualization.

## Build & Run

```bash
# Build the binary
go build -o github-feed main.go db.go

# Run directly (fetches from GitHub API)
./github-feed

# Run with flags
./github-feed --months 3      # Show items from last 3 months (default: 1)
./github-feed --debug          # Show detailed API logging instead of progress bar
./github-feed --local          # Use local database instead of GitHub API (offline mode)
./github-feed --links          # Show hyperlinks underneath each PR/issue
./github-feed --allowed-repos="owner/repo1,owner/repo2"  # Filter to specific repos
```

## Configuration

The tool requires a GitHub Personal Access Token with `repo` and `read:org` scopes (not needed in `--local` mode). Configuration is loaded from:
1. Environment variables: `GITHUB_TOKEN` or `GITHUB_ACTIVITY_TOKEN`, and `GITHUB_USERNAME` or `GITHUB_USER`
2. Config file at `~/.github-feed/.env` (automatically created on first run)

Database location: `~/.github-feed/github.db` (automatically created on first run)

**First Run**: The tool automatically creates `~/.github-feed/` directory with:
- `.env` file with template for credentials
- `github.db` database for caching GitHub data

## Architecture & Key Components

### Data Flow

#### Online Mode (Default)
1. **Parallel API Fetching** (main.go:~440-480): Seven concurrent GitHub search queries for PRs (authored, mentioned, assigned, commented, reviewed, review-requested, involved) plus event polling
2. **Issue Collection** (main.go:~485-515): Five parallel searches for issues with similar categories
3. **Database Caching** (db.go): All fetched PRs, issues, and comments are automatically saved to `~/.github-feed/github.db` BBolt database
4. **Cross-Reference Detection** (main.go:~520-565): Links issues to PRs by checking PR/issue bodies and comments for references
5. **Display Rendering** (main.go:~641-712): Separates items into sections by state (open/merged/closed) with colorized output
6. **Progress Tracking**: Dynamic progress bar that adjusts total count as pagination and additional API calls are discovered

#### Offline Mode (`--local`)
1. **Database Loading**: Reads all PRs and issues from `~/.github-feed/github.db` instead of making API calls
2. **Data Conversion**: Converts database records to PRActivity and IssueActivity structures
3. **Display Rendering**: Same rendering logic as online mode, showing all cached data
4. **No API Calls**: Completely offline, no GitHub token required

### Core Data Structures

**PRActivity** (main.go:21-29): Represents a PR with metadata:
- Label: How the user is involved (e.g., "Authored", "Reviewed")
- Owner/Repo: Repository identification
- PR: GitHub PullRequest object
- UpdatedAt: Last update timestamp
- HasUpdates: True if API version is newer than cached version
- Issues: Linked issues that reference this PR

**IssueActivity** (main.go:32-39): Represents an issue with similar metadata structure

**Progress** (main.go:42-95): Thread-safe progress tracking with colored bar display:
- Dynamically adjusts total as pagination and additional API calls are discovered
- Updates in real-time across all goroutines
- Provides visual feedback with color-coded completion status (red/yellow/green)

### Key Functions

**fetchAndDisplayActivity** (main.go:408-712): Main orchestration function that:
- Checks GitHub API rate limits
- Launches parallel PR/issue searches with dynamic progress tracking
- Performs cross-reference detection
- Sorts and displays results by state (open/merged/closed)
- Accepts `showLinks` parameter to optionally display URLs

**areCrossReferenced** (main.go:714-777): Determines if a PR and issue reference each other by:
- Checking PR/issue body text for mentions
- Fetching and checking PR comments for issue references (adds to progress total dynamically)
- Uses mentionsNumber() to detect patterns like "#123", "fixes #123", or full GitHub URLs
- Returns early if mention found in bodies (no API call needed)

**collectSearchResults** (main.go:1004-1205): Generic function for PR search that:
- Paginates through GitHub search results
- Dynamically adds additional pages to progress total when discovered
- Deduplicates using seenPRs map
- Fetches PR details for each result (adds to progress total)
- Updates progress tracker after each API call
- Caches all data to database for offline mode

**displayPR** (main.go:1207-1236): Displays a PR with:
- Color-coded label, username, and date
- Update indicator (● icon) if item has updates
- Optional hyperlink with 🔗 icon (when `--links` flag is used)

**displayIssue** (main.go:1238-1277): Displays an issue with:
- Similar formatting to displayPR
- Proper indentation for nested issues (under PRs)
- Optional hyperlink with proper indentation

**Color System** (main.go:96-152): Consistent color coding:
- getUserColor(): Hash-based consistent colors per username
- getLabelColor(): Fixed colors for involvement types
- getStateColor(): Fixed colors for PR/issue states

### Concurrency Patterns

The codebase uses `sync.WaitGroup` with goroutines (via `.Go()` method) for parallel API calls. All concurrent access to shared maps (seenPRs, seenIssues) is protected by mutex locks.

Progress tracking is thread-safe (main.go:48-58) and updated after each API call across all goroutines. The progress bar dynamically adjusts its total as new work is discovered.

### Date Filtering

Controlled by `--months` flag (default: 1):
- Shows both open and closed items updated in the specified time period
- Default: Items updated in last month
- Custom: `--months X` shows items from last X months
- No separate state filtering - shows all states (open/merged/closed) from the time period

## GitHub API Integration

Uses `google/go-github/v57` library. Key API patterns:
- Search API for bulk queries: `client.Search.Issues()`
- Events API for recent activity: `client.Activity.ListEventsPerformedByUser()`
- PullRequests API for details: `client.PullRequests.Get()`
- Comments API for cross-references: `client.PullRequests.ListComments()`
- Rate limit checking (main.go:364-405): Monitors both core (5000/hr) and search (30/min) limits

**Progress Bar Accuracy**: The progress bar now accurately tracks all API calls:
- Initial total includes base searches (12-15 calls depending on mode)
- Dynamically adds pagination when discovered on first page
- Dynamically adds PR detail fetches as needed
- Dynamically adds comment fetches during cross-reference checks
- Each API call increments counter immediately after completion

## Database Module (db.go)

**Database** structure wraps BBolt operations with three buckets:
- `pull_requests`: Stores PRs with key format `owner/repo#number`
- `issues`: Stores issues with same key format
- `comments`: Stores PR/issue comments with key format `owner/repo#number/type/commentID`

Key functions:
- `SavePullRequest`, `SaveIssue`, `SaveComment`: Update or insert items
- `GetAllPullRequests`, `GetAllIssues`: Retrieve all items for offline mode
- `Stats`: Returns count of items in each bucket

## Command-Line Flags

- `--months X`: Show items from last X months (default: 1)
- `--debug`: Show detailed API logging instead of progress bar
- `--local`: Use local database instead of GitHub API (offline mode, no token required)
- `--links`: Show hyperlinks (🔗) underneath each PR/issue
- `--allowed-repos REPOS`: Filter to specific repositories (comma-separated: `owner/repo1,owner/repo2`)

## Testing Considerations

When modifying this codebase:
- Test with both `--debug` and default modes (progress bar vs. detailed logs)
- Test `--local` mode to ensure database reads work correctly
- Test `--links` flag to verify URLs are displayed correctly with proper indentation
- Verify mutex protection on any new shared data structures
- Test cross-reference detection with various mention patterns (see mentionsNumber main.go:~779-822)
- Consider GitHub API rate limits when adding new API calls
- When adding new API calls, ensure progress bar is updated:
  - Add to total before making the call (`progress.addToTotal(1)`)
  - Increment after call completes (`progress.increment()`)
- Ensure database writes don't fail silently (currently errors are ignored with `_`)
- Verify `~/.github-feed/` directory is created on first run with proper permissions
