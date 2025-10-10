# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GitAI is a Go CLI tool for monitoring GitHub pull requests and issues across repositories. It tracks contributions, reviews, and assignments with colorized output and real-time progress visualization.

## Build & Run

```bash
# Build the binary
go build -o gitai main.go db.go

# Run directly (fetches from GitHub API)
./gitai

# Run with flags
./gitai --closed        # Include closed/merged items from last month
./gitai --debug         # Show detailed API logging instead of progress bar
./gitai --local         # Use local database instead of GitHub API (offline mode)
./gitai --env PATH      # Specify custom .env file path
```

## Configuration

The tool requires a GitHub Personal Access Token with `repo` and `read:org` scopes (not needed in `--local` mode). Configuration is loaded from:
1. Environment variables: `GITHUB_TOKEN` or `GITHUB_ACTIVITY_TOKEN`, and `GITHUB_USERNAME` or `GITHUB_USER`
2. Config file at `.gitai.env` in the program directory (default)
3. Custom config file specified with `--env PATH` flag

Database location: `gitai.db` in the same directory as the executable

## Architecture & Key Components

### Data Flow

#### Online Mode (Default)
1. **Parallel API Fetching** (main.go:~370-405): Seven concurrent GitHub search queries for PRs (authored, mentioned, assigned, commented, reviewed, review-requested, involved) plus event polling
2. **Issue Collection** (main.go:~420-442): Five parallel searches for issues with similar categories
3. **Database Caching** (db.go): All fetched PRs, issues, and comments are automatically saved to BBolt database
4. **Cross-Reference Detection** (main.go:~444-497): Links issues to PRs by checking PR/issue bodies and comments for references
5. **Display Rendering** (main.go:~556-626): Separates items into sections by state (open/merged/closed) with colorized output

#### Offline Mode (`--local`)
1. **Database Loading** (main.go:321-490 - `fetchAndDisplayFromDatabase`): Reads all PRs and issues from local database
2. **Data Conversion**: Converts database records to PRActivity and IssueActivity structures
3. **Display Rendering**: Same rendering logic as online mode, but with "CACHED" labels

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

## Database Module (db.go)

**Database** structure wraps BBolt operations with three buckets:
- `pull_requests`: Stores PRs with key format `owner/repo#number`
- `issues`: Stores issues with same key format
- `comments`: Stores PR/issue comments with key format `owner/repo#number/type/commentID`

Key functions:
- `SavePullRequest`, `SaveIssue`, `SaveComment`: Update or insert items
- `GetAllPullRequests`, `GetAllIssues`: Retrieve all items for offline mode
- `Stats`: Returns count of items in each bucket

## Testing Considerations

When modifying this codebase:
- Test with both `--debug` and default modes (progress bar vs. detailed logs)
- Test `--local` mode to ensure database reads work correctly
- Verify mutex protection on any new shared data structures
- Test cross-reference detection with various mention patterns (see mentionsNumber main.go:~675-715)
- Consider GitHub API rate limits when adding new API calls
- Ensure database writes don't fail silently (currently errors are ignored with `_`)
