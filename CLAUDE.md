# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GitHub Feed is a Go CLI tool for monitoring GitHub pull requests and issues across repositories. It tracks contributions, reviews, and assignments with colorized output and real-time progress visualization.

The tool is also called "GitAI" in the README (branding name), but the binary is `github-feed`.

## Build & Run

```bash
# Build the binary
go build -o github-feed .

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
- `.env` file with template for credentials (permissions: 0600)
- `github.db` database for caching GitHub data (permissions: 0666)
- Directory permissions: 0755

## Architecture & Key Components

### Data Flow

#### Online Mode (Default)
1. **Parallel API Fetching**: Six concurrent GitHub search queries for PRs (authored, mentioned, assigned, commented, reviewed, review-requested) - note: no "involved" query exists
2. **Issue Collection**: Four parallel searches for issues (authored, mentioned, assigned, commented)
3. **Database Caching** (db.go): All fetched PRs, issues, and comments are automatically saved to `~/.github-feed/github.db` BBolt database
4. **Cross-Reference Detection**: Links issues to PRs by checking PR/issue bodies and comments for references
5. **Display Rendering**: Separates items into sections by state (open/closed for PRs with merged as subset of closed) with colorized output
6. **Progress Tracking**: Dynamic progress bar that adjusts total count as pagination and additional API calls are discovered
7. **Error Handling**: Infinite retry with exponential backoff for all API calls, handling rate limits gracefully

#### Offline Mode (`--local`)
1. **Database Loading**: Reads all PRs and issues from `~/.github-feed/github.db` instead of making API calls
2. **Data Conversion**: Converts database records to PRActivity and IssueActivity structures
3. **Display Rendering**: Same rendering logic as online mode, showing all cached data
4. **No API Calls**: Completely offline, no GitHub token required

### Core Data Structures

**Config**: Global configuration structure (main.go:46-57):
- Consolidates all application settings (debug mode, local mode, time range, etc.)
- Shared across the application via global `config` variable
- Includes client, database, progress, and context references
- Fields: debugMode, localMode, showLinks, timeRange, username, allowedRepos (map[string]bool), client, db, progress, ctx

**PRActivity**: Represents a PR with metadata (main.go:22-30):
- Label: How the user is involved (e.g., "Authored", "Reviewed")
- Owner/Repo: Repository identification
- PR: GitHub PullRequest object (pointer to github.PullRequest)
- UpdatedAt: Last update timestamp (time.Time)
- HasUpdates: True if API version is newer than cached version
- Issues: Slice of linked IssueActivity that reference this PR

**IssueActivity**: Represents an issue with similar metadata structure (main.go:32-39):
- Label: How the user is involved
- Owner/Repo: Repository identification
- Issue: GitHub Issue object (pointer to github.Issue)
- UpdatedAt: Last update timestamp
- HasUpdates: True if API version is newer than cached version

**Progress**: Thread-safe progress tracking with colored bar display (main.go:41-44):
- Uses `atomic.Int32` for both `current` and `total` fields (no mutexes needed)
- Dynamically adjusts total as pagination and additional API calls are discovered
- Updates in real-time across all goroutines
- Provides visual feedback with color-coded completion status (red <33%, yellow <66%, green >=66%)
- Supports warning messages during retries via `displayWithWarning()` method (main.go:149-159)
- Methods: increment(), addToTotal(n int), buildBar(), display(), displayWithWarning(message string)

**Database**: BBolt wrapper providing structured storage (db.go:20-22):
- PRWithLabel and IssueWithLabel: Wraps items with their activity labels
- Supports both old format (without labels) and new format (with labels) for backwards compatibility

### Key Functions

**getPRLabelPriority / getIssueLabelPriority**: Label priority functions (main.go:61-87):
- Define priority ordering for PR labels: Authored(1) > Assigned(2) > Reviewed(3) > Review Requested(4) > Commented(5) > Mentioned(6)
- Define priority ordering for issue labels: Authored(1) > Assigned(2) > Commented(3) > Mentioned(4)
- Unknown labels get priority 999 (lowest)
- Used by `shouldUpdateLabel()` to determine if a label should be replaced

**shouldUpdateLabel**: Determines if a label should be updated (main.go:89-104):
- Takes current label, new label, and isPR flag
- Returns true if new label has higher priority (lower number)
- Empty current labels always get updated
- Ensures PRs/issues always show their most important involvement type
- Tested in priority_test.go

**fetchAndDisplayActivity**: Main orchestration function (main.go:603-852):
- Checks GitHub API rate limits before starting (unless in local mode)
- Initial progress total: 10 (6 PR queries + 4 issue queries) in online mode, or 10 in offline mode
- In online mode, does NOT add event polling to progress (events API not used in current code)
- Launches parallel PR/issue searches with dynamic progress tracking
- Performs cross-reference detection to link issues with PRs
- Sorts and displays results by state (open PRs, closed/merged PRs, open issues, closed issues)
- Uses global `config` for all settings
- Handles both online (API) and offline (database) modes

**retryWithBackoff**: Wraps API calls with infinite retry logic (main.go:161-247):
- Exponential backoff starting at 1s, max 30s
- Special handling for rate limit errors (longer backoff with factor 2.0, max 30s)
- General errors use shorter backoff (factor 1.5, max 5s)
- Shows countdown timer in progress bar during waits via `displayWithWarning()`
- Works seamlessly with both debug and normal modes
- Uses global `config.ctx` for cancellation support
- Detects rate limit errors by checking for "rate limit", "API rate limit exceeded", or "403" in error messages

**areCrossReferenced**: Determines if a PR and issue reference each other (main.go:854-923):
- Checks PR/issue body text for mentions first (fast path)
- Fetching and checking PR comments for issue references only if body check fails
- Uses mentionsNumber() to detect patterns like "#123", "fixes #123", or full GitHub URLs
- Returns early if mention found in bodies to avoid API call
- Adds API call to progress total dynamically only when needed
- In local mode, loads comments from database instead of API

**collectSearchResults**: Handles PR search with pagination (main.go:1151-1425):
- Supports both API mode (GitHub search) and local mode (database)
- In API mode: Paginates through GitHub search results, dynamically adds pages to progress
- In local mode: Filters by storedLabel matching the query label, respects timeRange cutoff
- Uses `sync.Map` for thread-safe deduplication (seenPRs and activitiesMap)
- Implements label priority system: only updates PR if new label has higher priority
- In API mode: Does NOT fetch full PR details separately - uses Issue data from search result directly
- Detects updates by comparing API timestamps with cached versions
- Caches all data to database with labels for offline mode
- Filters by allowed repos if configured

**collectIssueSearchResults**: Handles issue search (main.go:1493-1709):
- Same pattern as PR collection but for issues
- Filters out items with PullRequestLinks (actual PRs, not issues)
- Uses `sync.Map` for thread-safe deduplication
- Implements label priority system for issues
- Stores issues with their activity labels
- Detects updates by comparing API timestamps with cached versions
- In local mode: filters by storedLabel and timeRange

**collectActivityFromEvents**: ~~NOT CURRENTLY CALLED in the codebase~~ ✅ **REMOVED** - Dead code removed (was 186 lines)
- This function existed but was never invoked by fetchAndDisplayActivity
- Removed to reduce code complexity and maintainability burden

**displayPR**: Renders a PR with color-coded information (main.go:1241-1267):
- Formatted date, label, username, repo, and title
- Update indicator (yellow ● icon) if item has updates since last cache
- Optional hyperlink with 🔗 icon (when `--links` flag is used)
- Uses global `config.showLinks` setting
- Format: `[●] YYYY/MM/DD LABEL USERNAME owner/repo#NUM - title`

**displayIssue**: Renders an issue (main.go:1455-1491):
- Similar formatting to displayPR with proper indentation for nested issues
- State indicator (OPEN/CLOSED) when displayed under a PR (indented with "--")
- Update indicator (yellow ● icon) if item has updates since last cache
- Optional hyperlink with proper indentation
- Uses global `config.showLinks` setting

**Color System**: Consistent color coding throughout:
- `getUserColor()` (main.go:267-287): FNV hash-based consistent colors per username (11 color options from fatih/color)
- `getLabelColor()` (main.go:249-265): Fixed colors for involvement types (Authored=cyan, Mentioned=yellow, Assigned=magenta, Commented=blue, Reviewed=green, Review Requested=red, Involved=hi-black, Recent Activity=hi-cyan)
- `getStateColor()` (main.go:289-300): Fixed colors for PR/issue states (open=green, closed=red, merged=magenta)

**Helper Functions**:
- `loadEnvFile()` (main.go:302-325): Parses `.env` file and loads environment variables, skips comments and empty lines
- `parseTimeRange()` (main.go:327-357): Converts time range strings (e.g., "1h", "2d") to `time.Duration`
- `isRepoAllowed()` (main.go:549-555): Checks if a repository is in the allowed repos list
- `checkRateLimit()` (main.go:557-601): Checks GitHub API rate limits before making requests, warns when running low
- `mentionsNumber()` (main.go:925-962): Detects if text mentions a specific PR/issue number (supports patterns: #NUM, fixes #NUM, closes #NUM, resolves #NUM, GitHub URLs)

### Label Priority System

When a PR or issue appears in multiple search results (e.g., you both authored and reviewed a PR), the tool uses a priority system to determine which label to display:

**PR Label Priorities** (from highest to lowest):
1. Authored - You created the PR
2. Assigned - You're assigned to the PR
3. Reviewed - You reviewed the PR
4. Review Requested - Your review was requested
5. Commented - You commented on the PR
6. Mentioned - You were mentioned in the PR

**Issue Label Priorities** (from highest to lowest):
1. Authored - You created the issue
2. Assigned - You're assigned to the issue
3. Commented - You commented on the issue
4. Mentioned - You were mentioned in the issue

The system ensures that each PR/issue is displayed with its most important involvement type. When processing search results, labels are only updated if the new label has higher priority than the existing one. This prevents less important labels from overwriting more important ones.

### Concurrency Patterns

The codebase uses `sync.WaitGroup` with goroutines (via `.Go()` method) for parallel API calls:
- **PR Collection**: 6 parallel search queries (authored, mentioned, assigned, commented, reviewed, review-requested)
- **Issue Collection**: 4 parallel search queries (authored, mentioned, assigned, commented)
- **Cross-Reference Detection**: Parallel checking of PR-issue relationships using WaitGroup

All concurrent access to shared data is protected using modern Go patterns:
- **sync.Map**: `seenPRs`, `seenIssues`, `activitiesMap`, and `issueActivitiesMap` use `sync.Map` for lock-free concurrent access
- **atomic operations**: `Progress` struct uses `atomic.Int32` for `current` and `total` fields (no mutexes needed)
- **Conversion to slices**: After all goroutines complete, `sync.Map` data is converted to regular slices for sorting/display

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
- Parsing happens in main() via parseTimeRange() function

## GitHub API Integration

Uses `google/go-github/v57` library. Key API patterns:
- **Search API** for bulk queries: `client.Search.Issues()` - used for both PRs and issues (PRs are identified by PullRequestLinks field)
- **PullRequests API**: NOT used for individual PR fetching - PR data comes directly from search results
- **Comments API** for cross-references: `client.PullRequests.ListComments()` - PR comment bodies (only when checking cross-references)
- **Rate limit checking**: `client.RateLimit.Get()` monitors both core (5000/hr) and search (30/min) limits

**Error Handling**: All API calls wrapped with `retryWithBackoff()`:
- Infinite retries with exponential backoff
- Rate limit detection via error message inspection
- Countdown timer displayed during backoff periods
- Context-aware cancellation support

**Progress Bar Accuracy**: The progress bar accurately tracks all API calls:
- Initial total: 10 (6 PR searches + 4 issue searches) in online mode
- Dynamically adds pagination when discovered on first page of results (lastPage - 1 additional pages)
- Dynamically adds comment fetches during cross-reference checks (only when body mentions not found)
- Each API call increments counter immediately after completion
- Does NOT track individual PR detail fetches (as they don't happen)

## Database Module (db.go)

**Database** structure wraps BBolt operations with three buckets:
- `pull_requests`: Stores PRs with key format `owner/repo#number`
- `issues`: Stores issues with same key format
- `comments`: Stores PR/issue comments with key format `owner/repo#number/type/commentID`

**Data Format Evolution**:
- Old format: Direct JSON serialization of `github.PullRequest` / `github.Issue`
- New format: Wrapped in `PRWithLabel` / `IssueWithLabel` to store activity labels
- All read functions support both formats for backwards compatibility

**Key Database Functions**:

**Saving Data** (with labels, new format):
- `SavePullRequestWithLabel(owner, repo, pr, label, debugMode)` (db.go:90-120): Wraps PR in PRWithLabel, marshals to JSON, stores in pull_requests bucket
- `SaveIssueWithLabel(owner, repo, issue, label, debugMode)` (db.go:215-245): Same pattern for issues with IssueWithLabel
- `SavePRComment(owner, repo, prNumber, comment, debugMode)` (db.go:322-347): Stores PR review comments with key format `owner/repo#NUM/pr_review_comment/commentID`

**Saving Data** (legacy, without labels):
- `SavePullRequest(owner, repo, pr, debugMode)` (db.go:63-88): Legacy format, still supported
- `SaveIssue(owner, repo, issue, debugMode)` (db.go:188-213): Legacy format
- `SaveComment(owner, repo, itemNumber, comment, commentType)` (db.go:308-320): Generic comment storage

**Reading Data**:
- `GetPullRequest(owner, repo, number)` (db.go:122-146): Returns PR, attempts new format first, falls back to old format
- `GetPullRequestWithLabel(owner, repo, number)` (db.go:148-181): Returns PR and label, handles both formats
- `GetIssue(owner, repo, number)` (db.go:247-271): Returns issue, handles both formats
- `GetIssueWithLabel(owner, repo, number)` (db.go:273-306): Returns issue and label
- `GetPRComments(owner, repo, prNumber)` (db.go:573-595): Returns all PR comments using cursor-based prefix search
- `GetComment(owner, repo, itemNumber, commentType, commentID)` (db.go:349-366): Returns single comment

**Bulk Reading**:
- `GetAllPullRequests(debugMode)` (db.go:378-418): Returns map[string]*github.PullRequest, handles both formats
- `GetAllPullRequestsWithLabels(debugMode)` (db.go:420-465): Returns PRs and labels separately, used for offline mode
- `GetAllIssues(debugMode)` (db.go:467-507): Returns map[string]*github.Issue
- `GetAllIssuesWithLabels(debugMode)` (db.go:509-554): Returns issues and labels separately
- `GetAllComments()` (db.go:556-571): Returns all comment bodies as strings

**Utility**:
- `Stats()` (db.go:368-376): Returns counts of PRs, issues, and comments in database
- `Close()` (db.go:54-56): Closes BBolt database connection
- `OpenDatabase(path)` (db.go:24-52): Opens/creates database, creates buckets, sets permissions to 0666

## Command-Line Flags

Flags are parsed manually in main() (main.go:359-410):
- `--time RANGE`: Show items from last time range (default: `1m`)
  - Examples: `1h` (hour), `2d` (days), `3w` (weeks), `4m` (months), `1y` (year)
- `--debug`: Show detailed API logging instead of progress bar
- `--local`: Use local database instead of GitHub API (offline mode, no token required)
- `--links`: Show hyperlinks (🔗) underneath each PR/issue
- `--ll`: Shortcut for `--local --links` (offline mode with links) - IMPORTANT: This flag is NOT implemented in the code
- `--clean`: Delete and recreate the database cache (useful for starting fresh)
- `--allowed-repos REPOS`: Filter to specific repositories (comma-separated: `owner/repo1,owner/repo2`)

**NOTE**: The `--ll` flag is documented in README but NOT implemented in the command-line parsing logic. Only `--local` and `--links` work as separate flags.

## Testing Considerations

When modifying this codebase:
- **Mode Testing**: Test with both `--debug` and default modes (progress bar vs. detailed logs)
- **Offline Mode**: Test `--local` mode to ensure database reads work correctly
- **Link Display**: Test `--links` flag to verify URLs are displayed correctly with proper indentation
- **Concurrency Safety**: Verify atomic operations and `sync.Map` usage on any new shared data structures
- **Label Priority**: Test label updates to ensure priority system works correctly (see priority_test.go)
- **Cross-Reference Patterns**: Test with various mention patterns: `#123`, `fixes #123`, `closes #123`, GitHub URLs
- **Rate Limits**: Consider GitHub API rate limits when adding new API calls (5000/hr core, 30/min search)
- **Progress Tracking**: When adding new API calls, ensure progress bar is updated:
  - Add to total before making the call: `config.progress.addToTotal(1)`
  - Increment after call completes: `config.progress.increment()`
  - Display after each update: `config.progress.display()` (unless debug mode)
- **Error Handling**: Wrap all API calls with `retryWithBackoff()` for resilience
- **Database Errors**: Currently some database write errors are silently ignored with `_` - consider adding error logging
- **First Run**: Verify `~/.github-feed/` directory is created on first run with proper permissions (0755 for dir, 0600 for .env, 0666 for db)
- **Backwards Compatibility**: When changing database format, ensure old format can still be read
- **Global Config**: Use global `config` variable instead of passing parameters individually

## Testing

The project includes unit tests for critical functionality:

**priority_test.go**: Tests for label priority system
- `TestPRLabelPriority`: Validates PR label priority ordering (6 labels + unknown)
- `TestIssueLabelPriority`: Validates issue label priority ordering (4 labels + unknown)
- `TestShouldUpdateLabel_PR`: Tests PR label update logic with 7 test cases covering priority combinations
- `TestShouldUpdateLabel_Issue`: Tests issue label update logic with 7 test cases

Run tests with:
```bash
go test -v
go test -v -run TestPRLabelPriority  # Run specific test
```

## Known Issues & Discrepancies

1. ~~**`--ll` flag**: Documented in README but NOT implemented in code~~ ✅ **FIXED** - Flag now properly implemented (lines 395-397)
2. ~~**collectActivityFromEvents**: Function exists but is never called~~ ✅ **FIXED** - Dead code removed (186 lines)
3. **Initial progress total**: README mentions "7 PR queries" but actual code uses 6 PR queries
4. **PR detail fetching**: Code no longer fetches individual PR details via PullRequests.Get() - uses search result data directly
5. ~~**Database write errors**: Some database Save operations ignore errors~~ ✅ **FIXED** - Error logging added with atomic counter and summary warnings

## Refactoring Opportunities

Based on code analysis, potential improvements include:
1. ✅ **COMPLETED: Atomic Operations** - Progress now uses `atomic.Int32` instead of mutexes
2. ✅ **COMPLETED: sync.Map** - All shared maps now use `sync.Map` for lock-free concurrency
3. ✅ **COMPLETED: Progress Bar Logic** - `buildBar()` method extracts progress bar building logic (main.go:114-136)
4. **Implement `--ll` flag**: Add parsing for combined `--local --links` shortcut
5. **Remove collectActivityFromEvents**: Delete unused code or re-integrate if needed
6. **Error Handling**: Add logging for ignored database write errors
7. **Code Reuse**: Extract common patterns between collectSearchResults and collectIssueSearchResults (both follow same structure)
8. **Display Logic**: Unify displayPR and displayIssue with generic display function
9. **Database Operations**: Extract common patterns in GetAll* functions (all do similar iteration and format handling)
10. **Channels**: Consider replacing WaitGroups with result channels for cleaner coordination

## File Structure

```
github-feed/
├── main.go                      # Main application (1710 lines)
├── db.go                        # Database operations (596 lines)
├── priority_test.go             # Unit tests (106 lines)
├── go.mod                       # Go module definition
├── go.sum                       # Go module checksums
├── README.md                    # User documentation
├── CLAUDE.md                    # This file (AI assistant instructions)
├── .goreleaser.yml              # GoReleaser configuration for builds
├── .gitignore                   # Git ignore patterns
└── .github/
    └── workflows/
        └── release.yml          # GitHub Actions workflow for releases

~/.github-feed/                  # Config directory (auto-created)
 ├── .env                        # Configuration file with credentials
 └── github.db                   # BBolt database for caching
```
