# GitAI - GitHub Activity Monitor

A fast, colorful CLI tool for monitoring GitHub pull requests and issues across repositories. Track your contributions, reviews, and assignments with real-time progress visualization.

## Features

- 🚀 **Parallel API Calls** - Fetches data concurrently for maximum speed
- 🎨 **Colorized Output** - Easy-to-read color-coded labels, states, and progress
- 📊 **Smart Cross-Referencing** - Automatically links related PRs and issues
- ⚡ **Real-Time Progress Bar** - Visual feedback with color-coded completion status
- 🔍 **Comprehensive Search** - Tracks authored, mentioned, assigned, commented, and reviewed items
- 📅 **Time Filtering** - View items from the last 6 months (configurable with `--months`)
- 🎯 **Organized Display** - Separates open, merged, and closed items into clear sections

## Installation

### Build from Source

```bash
go build -o github-feed .
```

## Configuration

### First Run Setup

On first run, GitAI automatically creates a configuration directory at `~/.github-feed/` with:
- `.env` - Configuration file (with helpful template)
- `github.db` - Local database for caching GitHub data

### GitHub Token Setup

Create a GitHub Personal Access Token with the following scopes:
- `repo` - Access to repositories
- `read:org` - Read organization data

**Generate token:** https://github.com/settings/tokens

### Environment Setup

You can provide your token and username in two ways:

**Option 1: Configuration File (Recommended)**

Edit `~/.github-feed/.env` and add your credentials:
```bash
# Your GitHub Personal Access Token (required)
GITHUB_TOKEN=your_token_here

# Your GitHub username (required)
GITHUB_USERNAME=your_username

# Optional: Comma-separated list of allowed repos
ALLOWED_REPOS=user/repo1,user/repo2
```

**Option 2: Environment Variables**
```bash
export GITHUB_TOKEN="your_token_here"
export GITHUB_USERNAME="your_username"
export ALLOWED_REPOS="user/repo1,user/repo2"  # Optional: filter to specific repos
```

**Note:** Environment variables take precedence over the `.env` file.

## Usage

### Basic Usage

```bash
# Monitor PRs and issues from the last 6 months (default, fetches from GitHub)
gitai

# Show items from the last 3 months
gitai --months 3

# Show items from the last 12 months
gitai --months=12

# Show detailed logging output
gitai --debug

# Use local database instead of GitHub API (offline mode)
gitai --local

# Filter to specific repositories only
gitai --allowed-repos="user/repo1,user/repo2"

# Combine flags
gitai --local --months 12 --debug --allowed-repos="miniohq/ec,tunnels-is/tunnels"
```

### Command Line Options

| Flag | Description |
|------|-------------|
| `--months MONTHS` | Show items from the last X months, both open and closed (default: 6) |
| `--debug` | Show detailed API call progress instead of progress bar |
| `--local` | Use local database instead of GitHub API (offline mode, no token required) |
| `--allowed-repos REPOS` | Filter to specific repositories (comma-separated: `user/repo1,user/repo2`) |

### Color Coding

**Labels:**
- `AUTHORED` - Cyan
- `MENTIONED` - Yellow
- `ASSIGNED` - Magenta
- `COMMENTED` - Blue
- `REVIEWED` - Green
- `REVIEW REQUESTED` - Red
- `INVOLVED` - Gray

**States:**
- `OPEN` - Green
- `CLOSED` - Red
- `MERGED` - Magenta

**Usernames:** Each user gets a consistent color based on hash

## How It Works

### Online Mode (Default)

1. **Parallel Fetching** - Simultaneously searches for:
   - PRs you authored
   - PRs where you're mentioned
   - PRs assigned to you
   - PRs you commented on
   - PRs you reviewed
   - PRs requesting your review
   - PRs involving you
   - Your recent activity events
   - Issues you authored/mentioned/assigned/commented

2. **Local Caching** - All fetched data is automatically saved to a local BBolt database (`~/.github-feed/github.db`)
   - PRs, issues, and comments are cached for offline access
   - Each item is stored/updated with a unique key
   - Database grows as you fetch more data

3. **Cross-Reference Detection** - Automatically finds connections between PRs and issues by:
   - Checking PR body and comments for issue references (`#123`, `fixes #123`, full URLs)
   - Checking issue body and comments for PR references
   - Displaying linked issues directly under their related PRs

4. **Smart Filtering**:
   - Shows both open and closed items from the specified time period
   - **Default**: Items updated in last 6 months
   - **Custom** (`--months X`): Items updated in last X months

### Offline Mode (`--local`)

- Reads all data from the local database instead of GitHub API
- No internet connection or GitHub token required
- Displays all cached PRs and issues
- Useful for:
  - Working offline
  - Faster lookups when you don't need fresh data
  - Reviewing previously fetched data

## API Rate Limits

GitAI monitors GitHub API rate limits and will warn you when running low:
- **Search API**: 30 requests per minute
- **Core API**: 5000 requests per hour

Rate limit status is displayed in debug mode.

## Troubleshooting

### "GITHUB_TOKEN environment variable is required"
Set up your GitHub token as described in [Configuration](#configuration).

### "Rate limit exceeded"
Wait for the rate limit to reset. Use `--debug` to see current rate limits.

### Progress bar looks garbled
Your terminal may not support ANSI colors properly. Use `--debug` mode for plain text output.

## Development

### Project Structure
```
github-feed/
├── main.go                      # Main application code
├── db.go                        # Database operations for caching GitHub data
├── README.md                    # This file

~/.github-feed/              # Config directory (auto-created)
 ├── .env                     # Configuration file with credentials
 └── github.db                # BBolt database for caching
```

## License

MIT License - Feel free to use and modify as needed.

