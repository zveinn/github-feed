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
./github-feed --time 3h        # Show items from last 3 hours
./github-feed --time 2d        # Show items from last 2 days
./github-feed --time 3w        # Show items from last 3 weeks
./github-feed --time 6m        # Show items from last 6 months (default: 1m)
./github-feed --time 1y        # Show items from last year
./github-feed --debug          # Show detailed API logging instead of progress bar
./github-feed --local          # Use local database instead of GitHub API (offline mode)
./github-feed --links          # Show hyperlinks underneath each PR/issue
./github-feed --ll             # Shortcut for --local --links (offline mode with links)
./github-feed --clean          # Delete and recreate the database cache
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
1. **Parallel API Fetching**: Seven concurrent GitHub search queries for PRs (authored, mentioned, assigned, commented, reviewed, review-requested, involved) plus event polling
2. **Issue Collection**: Five parallel searches for issues with similar categories
3. **Database Caching** (db.go): All fetched PRs, issues, and comments are automatically saved to `~/.github-feed/github.db` BBolt database
4. **Cross-Reference Detection**: Links issues to PRs by checking PR/issue bodies and comments for references
5. **Display Rendering**: Separates items into sections by state (open/merged/closed) with colorized output
6. **Progress Tracking**: Dynamic progress bar that adjusts total count as pagination and additional API calls are discovered
7. **Error Handling**: Infinite retry with exponential backoff for all API calls, handling rate limits gracefully

#### Offline Mode (`--local`)
1. **Database Loading**: Reads all PRs and issues from `~/.github-feed/github.db` instead of making API calls
2. **Data Conversion**: Converts database records to PRActivity and IssueActivity structures
3. **Display Rendering**: Same rendering logic as online mode, showing all cached data
4. **No API Calls**: Completely offline, no GitHub token required

### Core Data Structures

**PRActivity**: Represents a PR with metadata:
- Label: How the user is involved (e.g., "Authored", "Reviewed")
- Owner/Repo: Repository identification
- PR: GitHub PullRequest object
- UpdatedAt: Last update timestamp
- HasUpdates: True if API version is newer than cached version
- Issues: Linked issues that reference this PR

**IssueActivity**: Represents an issue with similar metadata structure

**Progress**: Thread-safe progress tracking with colored bar display:
- Dynamically adjusts total as pagination and additional API calls are discovered
- Updates in real-time across all goroutines
- Provides visual feedback with color-coded completion status (red/yellow/green)
- Uses mutex for thread-safe counter operations

**Database**: BBolt wrapper providing structured storage:
- PRWithLabel and IssueWithLabel: Wraps items with their activity labels
- Supports both old format (without labels) and new format (with labels) for backwards compatibility

### Key Functions

**fetchAndDisplayActivity**: Main orchestration function that:
- Checks GitHub API rate limits before starting
- Launches parallel PR/issue searches with dynamic progress tracking
- Performs cross-reference detection to link issues with PRs
- Sorts and displays results by state (open/merged/closed)
- Accepts `showLinks` parameter to optionally display URLs
- Handles both online (API) and offline (database) modes

**retryWithBackoff**: Wraps API calls with infinite retry logic:
- Exponential backoff starting at 1s, max 30s
- Special handling for rate limit errors (longer backoff)
- Shows countdown timer in progress bar during waits
- Works seamlessly with both debug and normal modes

**areCrossReferenced**: Determines if a PR and issue reference each other by:
- Checking PR/issue body text for mentions first (fast path)
- Fetching and checking PR comments for issue references only if needed
- Uses mentionsNumber() to detect patterns like "#123", "fixes #123", or full GitHub URLs
- Returns early if mention found in bodies to avoid API call
- Adds API call to progress total dynamically only when needed

**collectSearchResults**: Handles PR search with pagination:
- Supports both API mode (GitHub search) and local mode (database)
- Paginates through GitHub search results in API mode
- Dynamically adds additional pages to progress total when discovered
- Deduplicates using seenPRs map with mutex protection
- Fetches PR details for each result and adds to progress total
- Detects updates by comparing API timestamps with cached versions
- Caches all data to database with labels for offline mode

**collectIssueSearchResults**: Handles issue search (similar to collectSearchResults):
- Same pattern as PR collection but for issues
- Filters out items with PullRequestLinks (actual PRs, not issues)
- Stores issues with their activity labels

**collectActivityFromEvents**: Fetches recent PR activity from Events API:
- Processes up to 3 pages of user events (300 events)
- Filters for PR-related event types
- Extracts PR numbers from event payloads
- Deduplicates and fetches PR details
- Labels found PRs as "Recent Activity"

**displayPR**: Renders a PR with color-coded information:
- Formatted date, label, username, repo, and title
- Update indicator (● icon) if item has updates since last cache
- Optional hyperlink with 🔗 icon (when `--links` flag is used)

**displayIssue**: Renders an issue (similar to displayPR):
- Similar formatting with proper indentation for nested issues
- State indicator when displayed under a PR
- Optional hyperlink with proper indentation

**Color System**: Consistent color coding throughout:
- getUserColor(): FNV hash-based consistent colors per username
- getLabelColor(): Fixed colors for involvement types
- getStateColor(): Fixed colors for PR/issue states (open/closed/merged)

### Concurrency Patterns

The codebase uses `sync.WaitGroup` with goroutines (via `.Go()` method) for parallel API calls:
- **PR Collection**: 7 parallel search queries + 1 event polling goroutine
- **Issue Collection**: 5 parallel search queries
- **Cross-Reference Detection**: Parallel checking of PR-issue relationships

All concurrent access to shared data is protected:
- `seenPRs` and `seenIssues` maps use dedicated mutexes (`seenPRsMu`, `seenIssuesMu`)
- `activities` and `issueActivities` slices use dedicated mutexes (`activitiesMu`, `issuesMu`)
- `Progress` struct uses internal mutex for counter operations

Progress tracking is thread-safe and updated after each API call across all goroutines. The progress bar dynamically adjusts its total as new work is discovered during execution.

### Time Filtering

Controlled by `--time` flag (default: `1m`):
- Shows both open and closed items updated in the specified time period
- Default: Items updated in last month (`1m` = 30 days)
- Supports flexible time ranges:
  - `h` = hours (e.g., `3h` = 3 hours)
  - `d` = days (e.g., `2d` = 2 days)
  - `w` = weeks (e.g., `3w` = 3 weeks)
  - `m` = months (e.g., `6m` = 6 months, approximated as 30 days each)
  - `y` = years (e.g., `1y` = 1 year, approximated as 365 days)
- No separate state filtering - shows all states (open/merged/closed) from the time period

## GitHub API Integration

Uses `google/go-github/v57` library. Key API patterns:
- **Search API** for bulk queries: `client.Search.Issues()` - used for both PRs and issues
- **Events API** for recent activity: `client.Activity.ListEventsPerformedByUser()` - up to 300 events
- **PullRequests API** for details: `client.PullRequests.Get()` - full PR information
- **Comments API** for cross-references: `client.PullRequests.ListComments()` - PR comment bodies
- **Rate limit checking**: `client.RateLimit.Get()` monitors both core (5000/hr) and search (30/min) limits

**Error Handling**: All API calls wrapped with `retryWithBackoff()`:
- Infinite retries with exponential backoff
- Rate limit detection via error message inspection
- Countdown timer displayed during backoff periods
- Context-aware cancellation support

**Progress Bar Accuracy**: The progress bar accurately tracks all API calls:
- Initial total: 12 searches (7 PR + 5 issue) + 3 event pages in online mode
- Dynamically adds pagination when discovered on first page of results
- Dynamically adds PR detail fetches as results are found
- Dynamically adds comment fetches during cross-reference checks (only when body mentions not found)
- Each API call increments counter immediately after completion

## Database Module (db.go)

**Database** structure wraps BBolt operations with three buckets:
- `pull_requests`: Stores PRs with key format `owner/repo#number`
- `issues`: Stores issues with same key format
- `comments`: Stores PR/issue comments with key format `owner/repo#number/type/commentID`

**Data Format Evolution**:
- Old format: Direct JSON serialization of `github.PullRequest` / `github.Issue`
- New format: Wrapped in `PRWithLabel` / `IssueWithLabel` to store activity labels
- All read functions support both formats for backwards compatibility

Key functions:
- `SavePullRequestWithLabel`, `SaveIssueWithLabel`: Store items with activity labels (new format)
- `SavePullRequest`, `SaveIssue`: Store items without labels (legacy, still supported)
- `SavePRComment`, `SaveComment`: Store comments for offline cross-reference checks
- `GetAllPullRequestsWithLabels`, `GetAllIssuesWithLabels`: Retrieve all items with labels for offline mode
- `GetPRComments`: Retrieve all comments for a PR using cursor-based prefix search
- `Stats`: Returns count of items in each bucket

## Command-Line Flags

- `--time RANGE`: Show items from last time range (default: `1m`)
  - Examples: `1h` (hour), `2d` (days), `3w` (weeks), `4m` (months), `1y` (year)
- `--debug`: Show detailed API logging instead of progress bar
- `--local`: Use local database instead of GitHub API (offline mode, no token required)
- `--links`: Show hyperlinks (🔗) underneath each PR/issue
- `--ll`: Shortcut for `--local --links` (offline mode with links)
- `--clean`: Delete and recreate the database cache (useful for starting fresh)
- `--allowed-repos REPOS`: Filter to specific repositories (comma-separated: `owner/repo1,owner/repo2`)

## Testing Considerations

When modifying this codebase:
- **Mode Testing**: Test with both `--debug` and default modes (progress bar vs. detailed logs)
- **Offline Mode**: Test `--local` mode to ensure database reads work correctly
- **Link Display**: Test `--links` flag to verify URLs are displayed correctly with proper indentation
- **Concurrency Safety**: Verify mutex/atomic protection on any new shared data structures
- **Cross-Reference Patterns**: Test with various mention patterns: `#123`, `fixes #123`, `closes #123`, GitHub URLs
- **Rate Limits**: Consider GitHub API rate limits when adding new API calls (5000/hr core, 30/min search)
- **Progress Tracking**: When adding new API calls, ensure progress bar is updated:
  - Add to total before making the call: `progress.addToTotal(1)`
  - Increment after call completes: `progress.increment()`
  - Display after each update: `progress.display()` (unless debug mode)
- **Error Handling**: Wrap all API calls with `retryWithBackoff()` for resilience
- **Database Errors**: Currently some database write errors are silently ignored with `_` - consider adding error logging
- **First Run**: Verify `~/.github-feed/` directory is created on first run with proper permissions (0755 for dir, 0600 for .env, 0666 for db)
- **Backwards Compatibility**: When changing database format, ensure old format can still be read

## Refactoring Opportunities

Based on code analysis, potential improvements include:
1. **Atomic Operations**: Replace Progress mutex with atomic int32/int64 operations
2. **sync.Map**: Replace seenPRs/seenIssues mutexes with sync.Map for better concurrency
3. **Channels**: Replace WaitGroups with result channels for cleaner coordination
4. **Code Reuse**: Extract common patterns between collectSearchResults and collectIssueSearchResults
5. **Display Logic**: Unify displayPR and displayIssue with generic display function
6. **Progress Bar**: Extract duplicated progress bar building logic into single function
7. **Database Operations**: Extract common patterns in GetAll* functions
