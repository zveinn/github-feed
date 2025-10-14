package main

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/google/go-github/v57/github"
)

type PRActivity struct {
	Label      string
	Owner      string
	Repo       string
	PR         *github.PullRequest
	UpdatedAt  time.Time
	HasUpdates bool
	Issues     []IssueActivity
}

type IssueActivity struct {
	Label      string
	Owner      string
	Repo       string
	Issue      *github.Issue
	UpdatedAt  time.Time
	HasUpdates bool
}

type Progress struct {
	current atomic.Int32
	total   atomic.Int32
}

type Config struct {
	debugMode    bool
	localMode    bool
	showLinks    bool
	timeRange    time.Duration
	username     string
	allowedRepos map[string]bool
	client       *github.Client
	db           *Database
	progress     *Progress
	ctx          context.Context
}

var config Config

func getPRLabelPriority(label string) int {
	priorities := map[string]int{
		"Authored":         1,
		"Assigned":         2,
		"Reviewed":         3,
		"Review Requested": 4,
		"Involved":         5,
		"Commented":        6,
		"Mentioned":        7,
	}
	if priority, ok := priorities[label]; ok {
		return priority
	}
	return 999 // Unknown labels get lowest priority
}

func getIssueLabelPriority(label string) int {
	priorities := map[string]int{
		"Authored":  1,
		"Assigned":  2,
		"Involved":  3,
		"Commented": 4,
		"Mentioned": 5,
	}
	if priority, ok := priorities[label]; ok {
		return priority
	}
	return 999 // Unknown labels get lowest priority
}

func shouldUpdateLabel(currentLabel, newLabel string, isPR bool) bool {
	if currentLabel == "" {
		return true
	}

	var currentPriority, newPriority int
	if isPR {
		currentPriority = getPRLabelPriority(currentLabel)
		newPriority = getPRLabelPriority(newLabel)
	} else {
		currentPriority = getIssueLabelPriority(currentLabel)
		newPriority = getIssueLabelPriority(newLabel)
	}

	return newPriority < currentPriority
}

func (p *Progress) increment() {
	p.current.Add(1)
}

func (p *Progress) addToTotal(n int) {
	p.total.Add(int32(n))
}

func (p *Progress) buildBar(current, total int32) (string, *color.Color, float64) {
	percentage := float64(current) / float64(total) * 100
	filled := int(percentage / 2)
	var barContent string
	for i := range 50 {
		if i < filled {
			barContent += "="
		} else if i == filled {
			barContent += ">"
		} else {
			barContent += " "
		}
	}
	var barColor *color.Color
	if percentage < 33 {
		barColor = color.New(color.FgRed)
	} else if percentage < 66 {
		barColor = color.New(color.FgYellow)
	} else {
		barColor = color.New(color.FgGreen)
	}
	return barContent, barColor, percentage
}

func (p *Progress) display() {
	current := p.current.Load()
	total := p.total.Load()
	barContent, barColor, percentage := p.buildBar(current, total)
	fmt.Printf("\r[%s] %s/%s (%s) ",
		barColor.Sprint(barContent),
		color.New(color.FgCyan).Sprint(current),
		color.New(color.FgCyan).Sprint(total),
		barColor.Sprintf("%.0f%%", percentage))
}

func (p *Progress) displayWithWarning(message string) {
	current := p.current.Load()
	total := p.total.Load()
	barContent, barColor, percentage := p.buildBar(current, total)
	fmt.Printf("\r[%s] %s/%s (%s) %s ",
		barColor.Sprint(barContent),
		color.New(color.FgCyan).Sprint(current),
		color.New(color.FgCyan).Sprint(total),
		barColor.Sprintf("%.0f%%", percentage),
		color.New(color.FgYellow).Sprint("! "+message))
}

func retryWithBackoff(operation func() error, operationName string) error {
	const (
		initialBackoff = 1 * time.Second
		maxBackoff     = 30 * time.Second
		backoffFactor  = 2.0
	)

	backoff := initialBackoff
	attempt := 1

	for {
		err := operation()
		if err == nil {
			return nil
		}

		isRateLimitError := strings.Contains(err.Error(), "rate limit") ||
			strings.Contains(err.Error(), "API rate limit exceeded") ||
			strings.Contains(err.Error(), "403")

		if isRateLimitError {
			waitTime := time.Duration(math.Min(float64(backoff), float64(maxBackoff)))

			if config.debugMode {
				fmt.Printf("  [%s] Rate limit hit (attempt %d), waiting %v before retry...\n",
					operationName, attempt, waitTime)
				select {
				case <-config.ctx.Done():
					return config.ctx.Err()
				case <-time.After(waitTime):
				}
			} else {
				ticker := time.NewTicker(1 * time.Second)
				defer ticker.Stop()

				remaining := int(waitTime.Seconds())
				for remaining > 0 {
					if config.progress != nil {
						config.progress.displayWithWarning(fmt.Sprintf("Rate limit hit, retrying in %ds", remaining))
					}

					select {
					case <-config.ctx.Done():
						return config.ctx.Err()
					case <-ticker.C:
						remaining--
					}
				}
			}

			backoff = time.Duration(float64(backoff) * backoffFactor)
		} else {
			waitTime := time.Duration(math.Min(float64(backoff)/2, float64(5*time.Second)))

			if config.debugMode {
				fmt.Printf("  [%s] Error (attempt %d): %v, waiting %v before retry...\n",
					operationName, attempt, err, waitTime)
				select {
				case <-config.ctx.Done():
					return config.ctx.Err()
				case <-time.After(waitTime):
				}
			} else {
				ticker := time.NewTicker(1 * time.Second)
				defer ticker.Stop()

				remaining := int(waitTime.Seconds())
				for remaining > 0 {
					if config.progress != nil {
						config.progress.displayWithWarning(fmt.Sprintf("API error, retrying in %ds", remaining))
					}

					select {
					case <-config.ctx.Done():
						return config.ctx.Err()
					case <-ticker.C:
						remaining--
					}
				}
			}

			backoff = time.Duration(float64(backoff) * 1.5)
		}

		attempt++
	}
}

func getLabelColor(label string) *color.Color {
	labelColors := map[string]*color.Color{
		"Authored":         color.New(color.FgCyan),
		"Mentioned":        color.New(color.FgYellow),
		"Assigned":         color.New(color.FgMagenta),
		"Commented":        color.New(color.FgBlue),
		"Reviewed":         color.New(color.FgGreen),
		"Review Requested": color.New(color.FgRed),
		"Involved":         color.New(color.FgHiBlack),
		"Recent Activity":  color.New(color.FgHiCyan),
	}

	if c, ok := labelColors[label]; ok {
		return c
	}
	return color.New(color.FgWhite)
}

func getUserColor(username string) *color.Color {
	h := fnv.New32a()
	h.Write([]byte(username))
	hash := h.Sum32()

	colors := []*color.Color{
		color.New(color.FgHiGreen),
		color.New(color.FgHiYellow),
		color.New(color.FgHiBlue),
		color.New(color.FgHiMagenta),
		color.New(color.FgHiCyan),
		color.New(color.FgHiRed),
		color.New(color.FgGreen),
		color.New(color.FgYellow),
		color.New(color.FgBlue),
		color.New(color.FgMagenta),
		color.New(color.FgCyan),
	}

	return colors[hash%uint32(len(colors))]
}

func getStateColor(state string) *color.Color {
	switch state {
	case "open":
		return color.New(color.FgGreen)
	case "closed":
		return color.New(color.FgRed)
	case "merged":
		return color.New(color.FgMagenta)
	default:
		return color.New(color.FgWhite)
	}
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}

func parseTimeRange(timeStr string) (time.Duration, error) {
	if len(timeStr) < 2 {
		return 0, fmt.Errorf("invalid time range format: %s (expected format like 1h, 2d, 3w, 4m, 1y)", timeStr)
	}

	numStr := timeStr[:len(timeStr)-1]
	unit := timeStr[len(timeStr)-1:]

	num, err := strconv.Atoi(numStr)
	if err != nil || num < 1 {
		return 0, fmt.Errorf("invalid time range number: %s (must be a positive integer)", numStr)
	}

	var duration time.Duration
	switch unit {
	case "h":
		duration = time.Duration(num) * time.Hour
	case "d":
		duration = time.Duration(num) * 24 * time.Hour
	case "w":
		duration = time.Duration(num) * 7 * 24 * time.Hour
	case "m":
		duration = time.Duration(num) * 30 * 24 * time.Hour
	case "y":
		duration = time.Duration(num) * 365 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid time unit: %s (use h=hours, d=days, w=weeks, m=months, y=years)", unit)
	}

	return duration, nil
}

func main() {
	var username string
	var timeRange time.Duration = 30 * 24 * time.Hour
	var debugMode bool
	var localMode bool
	var showLinks bool
	var allowedReposFlag string
	var cleanCache bool

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--time" || strings.HasPrefix(arg, "--time=") {
			var timeStr string
			if strings.HasPrefix(arg, "--time=") {
				timeStr = strings.TrimPrefix(arg, "--time=")
			} else if i+1 < len(os.Args) {
				timeStr = os.Args[i+1]
				i++ // Skip next argument
			} else {
				fmt.Println("Error: --time requires a value (e.g., --time 1h, --time 2d, --time 3w, --time 4m, --time 1y)")
				os.Exit(1)
			}

			duration, err := parseTimeRange(timeStr)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				fmt.Println("Examples: --time 1h (1 hour), --time 2d (2 days), --time 3w (3 weeks), --time 4m (4 months), --time 1y (1 year)")
				os.Exit(1)
			}
			timeRange = duration
		} else if arg == "--debug" {
			debugMode = true
		} else if arg == "--local" {
			localMode = true
		} else if arg == "--links" {
			showLinks = true
		} else if arg == "--clean" {
			cleanCache = true
		} else if arg == "--allowed-repos" || strings.HasPrefix(arg, "--allowed-repos=") {
			if strings.HasPrefix(arg, "--allowed-repos=") {
				allowedReposFlag = strings.TrimPrefix(arg, "--allowed-repos=")
			} else if i+1 < len(os.Args) {
				allowedReposFlag = os.Args[i+1]
				i++
			} else {
				fmt.Println("Error: --allowed-repos requires a comma-separated list of repos")
				os.Exit(1)
			}
		} else if !strings.HasPrefix(arg, "--") {
			username = arg
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error: Could not determine home directory: %v\n", err)
		os.Exit(1)
	}

	configDir := filepath.Join(homeDir, ".github-feed")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		fmt.Printf("Error: Could not create config directory %s: %v\n", configDir, err)
		os.Exit(1)
	}

	envPath := filepath.Join(configDir, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		envTemplate := `# GitHub Feed Configuration
# Add your GitHub credentials here

# Your GitHub Personal Access Token (required)
# Generate at: https://github.com/settings/tokens
# Required scopes: repo, read:org
GITHUB_TOKEN=

# Your GitHub username (required)
GITHUB_USERNAME=

# Optional: Comma-separated list of allowed repos (e.g., user/repo1,user/repo2)
# Leave empty to allow all repos
ALLOWED_REPOS=
`
		if err := os.WriteFile(envPath, []byte(envTemplate), 0o600); err != nil {
			fmt.Printf("Warning: Could not create .env file at %s: %v\n", envPath, err)
		}
	}

	_ = loadEnvFile(envPath)

	username = os.Getenv("GITHUB_USERNAME")
	if username == "" {
		username = os.Getenv("GITHUB_USER")
	}

	allowedReposStr := allowedReposFlag
	if allowedReposStr == "" {
		allowedReposStr = os.Getenv("ALLOWED_REPOS")
	}

	var allowedRepos map[string]bool
	if allowedReposStr != "" {
		allowedRepos = make(map[string]bool)
		repos := strings.Split(allowedReposStr, ",")
		for _, repo := range repos {
			repo = strings.TrimSpace(repo)
			if repo != "" {
				allowedRepos[repo] = true
			}
		}
		if debugMode && len(allowedRepos) > 0 {
			fmt.Printf("Filtering to allowed repositories: %v\n", allowedRepos)
		}
	}

	dbPath := filepath.Join(configDir, "github.db")

	if cleanCache {
		fmt.Println("Cleaning database cache...")
		if _, err := os.Stat(dbPath); err == nil {
			if err := os.Remove(dbPath); err != nil {
				fmt.Printf("Warning: Failed to delete database file: %v\n", err)
			} else {
				fmt.Println("Database cache cleaned successfully")
			}
		} else {
			fmt.Println("No existing database cache to clean")
		}
	}

	db, err := OpenDatabase(dbPath)
	if err != nil {
		fmt.Printf("Warning: Failed to open database: %v\n", err)
		fmt.Println("Continuing without database caching...")
		db = nil
	} else {
		defer db.Close()
	}

	token := os.Getenv("GITHUB_ACTIVITY_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" && !localMode {
		fmt.Println("Error: GITHUB_ACTIVITY_TOKEN or GITHUB_TOKEN environment variable is required")
		fmt.Println("\nTo generate a GitHub token:")
		fmt.Println("1. Go to https://github.com/settings/tokens")
		fmt.Println("2. Click 'Generate new token' -> 'Generate new token (classic)'")
		fmt.Println("3. Give it a name and select these scopes: 'repo', 'read:org'")
		fmt.Println("4. Generate and copy the token")
		fmt.Println("5. Export it: export GITHUB_TOKEN=your_token_here")
		fmt.Printf("6. Or add it to %s\n", envPath)
		os.Exit(1)
	}

	if username == "" && !localMode {
		fmt.Println("Error: Please provide a GitHub username")
		fmt.Println("Usage: github-feed [--time RANGE] [--debug] [--local] [--links] [--clean] [--allowed-repos REPOS] [username]")
		fmt.Println("  --time RANGE: Show items from the last time range (default: 1m)")
		fmt.Println("                Examples: 1h (1 hour), 2d (2 days), 3w (3 weeks), 4m (4 months), 1y (1 year)")
		fmt.Println("  --debug: Show detailed API progress")
		fmt.Println("  --local: Use local database instead of GitHub API")
		fmt.Println("  --links: Show hyperlinks underneath each PR/issue")
		fmt.Println("  --clean: Delete and recreate the database cache")
		fmt.Println("  --allowed-repos REPOS: Comma-separated list of allowed repos (e.g., user/repo1,user/repo2)")
		fmt.Println("Or set GITHUB_USERNAME environment variable")
		fmt.Printf("Or add it to %s\n", envPath)
		os.Exit(1)
	}

	if debugMode {
		fmt.Printf("Monitoring GitHub PR activity for user: %s\n", username)
		fmt.Printf("Showing items from the last %v\n", timeRange)
	}
	if debugMode {
		fmt.Println("Debug mode enabled")
	}

	config.debugMode = debugMode
	config.localMode = localMode
	config.showLinks = showLinks
	config.timeRange = timeRange
	config.username = username
	config.allowedRepos = allowedRepos
	config.db = db
	config.ctx = context.Background()
	config.client = github.NewClient(nil).WithAuthToken(token)

	fetchAndDisplayActivity()
}

func isRepoAllowed(owner, repo string) bool {
	if config.allowedRepos == nil || len(config.allowedRepos) == 0 {
		return true
	}
	repoKey := fmt.Sprintf("%s/%s", owner, repo)
	return config.allowedRepos[repoKey]
}

func checkRateLimit() error {
	var rateLimits *github.RateLimits
	var err error

	retryErr := retryWithBackoff(func() error {
		rateLimits, _, err = config.client.RateLimit.Get(config.ctx)
		return err
	}, "RateLimitCheck")

	if retryErr != nil {
		return fmt.Errorf("failed to fetch rate limit: %w", retryErr)
	}

	core := rateLimits.Core
	search := rateLimits.Search

	if config.debugMode {
		fmt.Printf("Rate Limits - Core: %d/%d, Search: %d/%d\n",
			core.Remaining, core.Limit,
			search.Remaining, search.Limit)
	}

	if core.Remaining == 0 {
		resetTime := core.Reset.Time.Sub(time.Now())
		fmt.Printf("WARNING: Core API rate limit exceeded! Resets in %v\n", resetTime.Round(time.Second))
		return fmt.Errorf("rate limit exceeded, resets at %v", core.Reset.Time.Format("15:04:05"))
	}

	if search.Remaining == 0 {
		resetTime := search.Reset.Time.Sub(time.Now())
		fmt.Printf("WARNING: Search API rate limit exceeded! Resets in %v\n", resetTime.Round(time.Second))
		return fmt.Errorf("search rate limit exceeded, resets at %v", search.Reset.Time.Format("15:04:05"))
	}

	coreThreshold := core.Limit / 5
	if core.Remaining < coreThreshold && core.Remaining > 0 {
		fmt.Printf("WARNING: Core API rate limit running low (%d remaining)\n", core.Remaining)
	}

	if search.Remaining < 5 && search.Remaining > 0 {
		fmt.Printf("WARNING: Search API rate limit running low (%d remaining)\n", search.Remaining)
	}

	return nil
}

func fetchAndDisplayActivity() {
	startTime := time.Now()

	if !config.localMode {
		if err := checkRateLimit(); err != nil {
			fmt.Printf("Skipping this cycle due to rate limit: %v\n", err)
			return
		}
		if config.debugMode {
			fmt.Println()
		}
	}

	var seenPRs sync.Map // Maps prKey -> label
	activitiesMap := sync.Map{} // Maps prKey -> *PRActivity

	initialTotal := 12
	if !config.localMode {
		initialTotal += 3
	}
	config.progress = &Progress{}
	config.progress.current.Store(0)
	config.progress.total.Store(int32(initialTotal))

	if config.debugMode {
		fmt.Println("Running optimized search queries...")
	} else {
		fmt.Print("Fetching data from GitHub... ")
		config.progress.display()
	}

	dateAgo := time.Now().Add(-config.timeRange).Format("2006-01-02")
	dateFilter := fmt.Sprintf("updated:>=%s", dateAgo)


	buildQuery := func(base string) string {
		return fmt.Sprintf("%s %s", base, dateFilter)
	}

	var prWg sync.WaitGroup

	prQueries := []struct {
		query string
		label string
	}{
		{buildQuery(fmt.Sprintf("is:pr reviewed-by:%s", config.username)), "Reviewed"},
		{buildQuery(fmt.Sprintf("is:pr review-requested:%s", config.username)), "Review Requested"},
		{buildQuery(fmt.Sprintf("is:pr author:%s", config.username)), "Authored"},
		{buildQuery(fmt.Sprintf("is:pr assignee:%s", config.username)), "Assigned"},
		{buildQuery(fmt.Sprintf("is:pr involves:%s", config.username)), "Involved"},
		{buildQuery(fmt.Sprintf("is:pr commenter:%s", config.username)), "Commented"},
		{buildQuery(fmt.Sprintf("is:pr mentions:%s", config.username)), "Mentioned"},
	}

	for _, pq := range prQueries {
		query := pq.query
		label := pq.label
		prWg.Go(func() {
			collectSearchResults(query, label, &seenPRs, &activitiesMap)
		})
	}

	if !config.localMode {
		prWg.Go(func() {
			collectActivityFromEvents(&seenPRs, &activitiesMap)
		})
	} else {
		prWg.Go(func() {
			collectSearchResults("", "Recent Activity", &seenPRs, &activitiesMap)
		})
	}

	prWg.Wait()

	if config.debugMode {
		fmt.Println()
		fmt.Println("Running issue search queries...")
	}
	var seenIssues sync.Map // Maps issueKey -> label
	issueActivitiesMap := sync.Map{} // Maps issueKey -> *IssueActivity

	var issueWg sync.WaitGroup

	issueQueries := []struct {
		query string
		label string
	}{
		{buildQuery(fmt.Sprintf("is:issue author:%s", config.username)), "Authored"},
		{buildQuery(fmt.Sprintf("is:issue mentions:%s", config.username)), "Mentioned"},
		{buildQuery(fmt.Sprintf("is:issue assignee:%s", config.username)), "Assigned"},
		{buildQuery(fmt.Sprintf("is:issue commenter:%s", config.username)), "Commented"},
		{buildQuery(fmt.Sprintf("is:issue involves:%s", config.username)), "Involved"},
	}

	for _, iq := range issueQueries {
		query := iq.query
		label := iq.label
		issueWg.Go(func() {
			collectIssueSearchResults(query, label, &seenIssues, &issueActivitiesMap)
		})
	}

	issueWg.Wait()

	// Convert activitiesMap to slice
	activities := []PRActivity{}
	activitiesMap.Range(func(key, value interface{}) bool {
		if activity, ok := value.(*PRActivity); ok {
			activities = append(activities, *activity)
		}
		return true
	})

	// Convert issueActivitiesMap to slice
	issueActivities := []IssueActivity{}
	issueActivitiesMap.Range(func(key, value interface{}) bool {
		if activity, ok := value.(*IssueActivity); ok {
			issueActivities = append(issueActivities, *activity)
		}
		return true
	})

	if config.debugMode {
		fmt.Println("Checking cross-references between PRs and issues...")
	}


	linkedIssues := make(map[string]bool)

	var wg sync.WaitGroup

	for j := range issueActivities {
		issue := &issueActivities[j]
		issueKey := fmt.Sprintf("%s/%s#%d", issue.Owner, issue.Repo, issue.Issue.GetNumber())

		for i := range activities {
			pr := &activities[i]
			if pr.Owner == issue.Owner && pr.Repo == issue.Repo {
				wg.Go(func() {
					if areCrossReferenced(pr, issue) {
						pr.Issues = append(pr.Issues, *issue)
						linkedIssues[issueKey] = true
						if config.debugMode {
							fmt.Printf("  Linked %s/%s#%d <-> %s/%s#%d\n",
								pr.Owner, pr.Repo, pr.PR.GetNumber(),
								issue.Owner, issue.Repo, issue.Issue.GetNumber())
						}
					}
				})
			}
		}
	}

	wg.Wait()

	standaloneIssues := []IssueActivity{}
	for _, issue := range issueActivities {
		issueKey := fmt.Sprintf("%s/%s#%d", issue.Owner, issue.Repo, issue.Issue.GetNumber())
		if !linkedIssues[issueKey] {
			standaloneIssues = append(standaloneIssues, issue)
		}
	}

	duration := time.Since(startTime)
	if config.debugMode {
		fmt.Println()
		fmt.Printf("Total fetch time: %v\n", duration.Round(time.Millisecond))
		fmt.Printf("Found %d unique PRs and %d unique issues\n", len(activities), len(issueActivities))

		if config.db != nil {
			prCount, issueCount, commentCount, err := config.db.Stats()
			if err == nil {
				fmt.Printf("Database stats: %d PRs, %d issues, %d comments\n", prCount, issueCount, commentCount)
			}
		}
		fmt.Println()
	} else {
		fmt.Print("\r" + strings.Repeat(" ", 80) + "\r")
	}

	if len(activities) == 0 && len(standaloneIssues) == 0 {
		fmt.Println("No open activity found")
		return
	}

	sort.Slice(activities, func(i, j int) bool {
		return activities[i].UpdatedAt.After(activities[j].UpdatedAt)
	})
	sort.Slice(standaloneIssues, func(i, j int) bool {
		return standaloneIssues[i].UpdatedAt.After(standaloneIssues[j].UpdatedAt)
	})

	var openPRs, closedPRs, mergedPRs []PRActivity
	for _, activity := range activities {
		if activity.PR.State != nil && *activity.PR.State == "closed" {
			if activity.PR.Merged != nil && *activity.PR.Merged {
				mergedPRs = append(mergedPRs, activity)
			} else {
				closedPRs = append(closedPRs, activity)
			}
		} else {
			openPRs = append(openPRs, activity)
		}
	}

	var openIssues, closedIssues []IssueActivity
	for _, issue := range standaloneIssues {
		if issue.Issue.State != nil && *issue.Issue.State == "closed" {
			closedIssues = append(closedIssues, issue)
		} else {
			openIssues = append(openIssues, issue)
		}
	}

	if len(openPRs) > 0 {
		titleColor := color.New(color.FgHiGreen, color.Bold)
		fmt.Println(titleColor.Sprint("OPEN PULL REQUESTS:"))
		fmt.Println("------------------------------------------")
		for _, activity := range openPRs {
			displayPR(activity.Label, activity.Owner, activity.Repo, activity.PR, activity.HasUpdates)
			if len(activity.Issues) > 0 {
				for _, issue := range activity.Issues {
					displayIssue(issue.Label, issue.Owner, issue.Repo, issue.Issue, true, issue.HasUpdates)
				}
			}
		}
	}

	if len(mergedPRs) > 0 {
		fmt.Println()
		titleColor := color.New(color.FgHiMagenta, color.Bold)
		fmt.Println(titleColor.Sprint("MERGED PULL REQUESTS:"))
		fmt.Println("------------------------------------------")
		for _, activity := range mergedPRs {
			displayPR(activity.Label, activity.Owner, activity.Repo, activity.PR, activity.HasUpdates)
			if len(activity.Issues) > 0 {
				for _, issue := range activity.Issues {
					displayIssue(issue.Label, issue.Owner, issue.Repo, issue.Issue, true, issue.HasUpdates)
				}
			}
		}
	}

	if len(closedPRs) > 0 {
		fmt.Println()
		titleColor := color.New(color.FgHiRed, color.Bold)
		fmt.Println(titleColor.Sprint("CLOSED PULL REQUESTS:"))
		fmt.Println("------------------------------------------")
		for _, activity := range closedPRs {
			displayPR(activity.Label, activity.Owner, activity.Repo, activity.PR, activity.HasUpdates)
			if len(activity.Issues) > 0 {
				for _, issue := range activity.Issues {
					displayIssue(issue.Label, issue.Owner, issue.Repo, issue.Issue, true, issue.HasUpdates)
				}
			}
		}
	}

	if len(openIssues) > 0 {
		fmt.Println()
		titleColor := color.New(color.FgHiGreen, color.Bold)
		fmt.Println(titleColor.Sprint("OPEN ISSUES:"))
		fmt.Println("------------------------------------------")
		for _, issue := range openIssues {
			displayIssue(issue.Label, issue.Owner, issue.Repo, issue.Issue, false, issue.HasUpdates)
		}
	}

	if len(closedIssues) > 0 {
		fmt.Println()
		titleColor := color.New(color.FgHiRed, color.Bold)
		fmt.Println(titleColor.Sprint("CLOSED ISSUES:"))
		fmt.Println("------------------------------------------")
		for _, issue := range closedIssues {
			displayIssue(issue.Label, issue.Owner, issue.Repo, issue.Issue, false, issue.HasUpdates)
		}
	}
}

func areCrossReferenced(pr *PRActivity, issue *IssueActivity) bool {
	prNumber := pr.PR.GetNumber()
	issueNumber := issue.Issue.GetNumber()

	if config.debugMode {
		fmt.Printf("  Checking cross-reference: PR %s/%s#%d <-> Issue %s/%s#%d\n",
			pr.Owner, pr.Repo, prNumber,
			issue.Owner, issue.Repo, issueNumber)
	}

	prBody := pr.PR.GetBody()
	if mentionsNumber(prBody, issueNumber, pr.Owner, pr.Repo) {
		return true
	}

	issueBody := issue.Issue.GetBody()
	if mentionsNumber(issueBody, prNumber, issue.Owner, issue.Repo) {
		return true
	}

	var prComments []*github.PullRequestComment
	var err error

	if config.localMode {
		if config.db != nil {
			prComments, err = config.db.GetPRComments(pr.Owner, pr.Repo, prNumber)
			if err != nil && config.debugMode {
				fmt.Printf("  Warning: Could not fetch comments from database for %s/%s#%d: %v\n",
					pr.Owner, pr.Repo, prNumber, err)
			}
		}
	} else {
		config.progress.addToTotal(1)
		if !config.debugMode {
			config.progress.display()
		}

		retryErr := retryWithBackoff(func() error {
			prComments, _, err = config.client.PullRequests.ListComments(config.ctx, pr.Owner, pr.Repo, prNumber, &github.PullRequestListCommentsOptions{
				ListOptions: github.ListOptions{PerPage: 100},
			})
			return err
		}, fmt.Sprintf("Comments-PR#%d", prNumber))

		config.progress.increment()
		if !config.debugMode {
			config.progress.display()
		}

		if retryErr != nil {
			err = retryErr
		}

		if err == nil && config.db != nil {
			for _, comment := range prComments {
				_ = config.db.SavePRComment(pr.Owner, pr.Repo, prNumber, comment, config.debugMode)
			}
		}
	}

	if err == nil {
		for _, comment := range prComments {
			if mentionsNumber(comment.GetBody(), issueNumber, pr.Owner, pr.Repo) {
				return true
			}
		}
	}

	return false
}

func mentionsNumber(text string, number int, owner string, repo string) bool {
	if text == "" {
		return false
	}

	lowerText := strings.ToLower(text)

	urlPatterns := []string{
		fmt.Sprintf("github.com/%s/%s/issues/%d", strings.ToLower(owner), strings.ToLower(repo), number),
		fmt.Sprintf("github.com/%s/%s/pull/%d", strings.ToLower(owner), strings.ToLower(repo), number),
	}
	for _, pattern := range urlPatterns {
		if strings.Contains(lowerText, pattern) {
			return true
		}
	}

	patterns := []string{
		fmt.Sprintf("#%d", number),
		fmt.Sprintf("fixes #%d", number),
		fmt.Sprintf("closes #%d", number),
		fmt.Sprintf("resolves #%d", number),
		fmt.Sprintf("fixed #%d", number),
		fmt.Sprintf("closed #%d", number),
		fmt.Sprintf("resolved #%d", number),
		fmt.Sprintf("fix #%d", number),
		fmt.Sprintf("close #%d", number),
		fmt.Sprintf("resolve #%d", number),
	}

	for _, pattern := range patterns {
		if strings.Contains(lowerText, pattern) {
			return true
		}
	}

	return false
}

func collectActivityFromEvents(seenPRs *sync.Map, activitiesMap *sync.Map) {
	opts := &github.ListOptions{PerPage: 100}

	if config.debugMode {
		fmt.Println("Checking recent activity events...")
	}
	totalPRs := 0

	for page := range 3 {
		if config.debugMode {
			fmt.Printf("  [Events] Fetching page %d...\n", page+1)
		}

		var events []*github.Event
		var resp *github.Response
		var err error

		retryErr := retryWithBackoff(func() error {
			events, resp, err = config.client.Activity.ListEventsPerformedByUser(config.ctx, config.username, false, opts)
			return err
		}, fmt.Sprintf("Events-page%d", page+1))

		config.progress.increment()
		if !config.debugMode {
			config.progress.display()
		}

		if retryErr != nil {
			fmt.Printf("Error fetching user events after retries: %v\n", retryErr)
			break
		}

		for _, event := range events {
			if event.Type == nil || event.Repo == nil {
				continue
			}

			eventType := *event.Type
			if eventType == "PullRequestEvent" ||
				eventType == "PullRequestReviewEvent" ||
				eventType == "PullRequestReviewCommentEvent" ||
				eventType == "IssueCommentEvent" {

				repoName := *event.Repo.Name
				parts := strings.Split(repoName, "/")
				if len(parts) != 2 {
					continue
				}
				owner, repo := parts[0], parts[1]

				if !isRepoAllowed(owner, repo) {
					continue
				}

				var prNumber int
				if eventType == "PullRequestEvent" && event.Payload() != nil {
					if prEvent, ok := event.Payload().(*github.PullRequestEvent); ok && prEvent.PullRequest != nil {
						prNumber = *prEvent.PullRequest.Number
					}
				} else if eventType == "PullRequestReviewEvent" && event.Payload() != nil {
					if reviewEvent, ok := event.Payload().(*github.PullRequestReviewEvent); ok && reviewEvent.PullRequest != nil {
						prNumber = *reviewEvent.PullRequest.Number
					}
				} else if eventType == "PullRequestReviewCommentEvent" && event.Payload() != nil {
					if commentEvent, ok := event.Payload().(*github.PullRequestReviewCommentEvent); ok && commentEvent.PullRequest != nil {
						prNumber = *commentEvent.PullRequest.Number
					}
				} else if eventType == "IssueCommentEvent" && event.Payload() != nil {
					if issueEvent, ok := event.Payload().(*github.IssueCommentEvent); ok && issueEvent.Issue != nil && issueEvent.Issue.IsPullRequest() {
						prNumber = *issueEvent.Issue.Number
					}
				}

				if prNumber > 0 {
					prKey := fmt.Sprintf("%s/%s#%d", owner, repo, prNumber)

					label := "Recent Activity"

					// Check if we've already processed this PR in activitiesMap
					existingActivity, alreadyProcessed := activitiesMap.Load(prKey)
					shouldProcess := true

					if alreadyProcessed {
						// PR is already in activitiesMap, check if we need to update the label
						existingPR := existingActivity.(*PRActivity)
						if shouldUpdateLabel(existingPR.Label, label, true) {
							// New label has higher priority, we'll update it
							if config.debugMode {
								fmt.Printf("  [Events] Updating label for %s from %s to %s (higher priority)\n", prKey, existingPR.Label, label)
							}
						} else {
							// Existing label has higher or equal priority, skip
							shouldProcess = false
						}
					}

					if shouldProcess {
						config.progress.addToTotal(1)
						if !config.debugMode {
							config.progress.display()
						}

						var pr *github.PullRequest
						var prErr error

						retryErr := retryWithBackoff(func() error {
							pr, _, prErr = config.client.PullRequests.Get(config.ctx, owner, repo, prNumber)
							return prErr
						}, fmt.Sprintf("Events-PR#%d", prNumber))

						config.progress.increment()
						if !config.debugMode {
							config.progress.display()
						}

						if retryErr != nil || pr.GetState() != "open" {
							continue
						}

						hasUpdates := false

						if config.db != nil {
							cachedPR, err := config.db.GetPullRequest(owner, repo, prNumber)
							if err == nil {
								if pr.GetUpdatedAt().After(cachedPR.GetUpdatedAt().Time) {
									hasUpdates = true
								}
							}
							_ = config.db.SavePullRequestWithLabel(owner, repo, pr, label, config.debugMode)
						}

						activity := PRActivity{
							Label:      label,
							Owner:      owner,
							Repo:       repo,
							PR:         pr,
							UpdatedAt:  pr.GetUpdatedAt().Time,
							HasUpdates: hasUpdates,
						}
						activitiesMap.Store(prKey, &activity)
						totalPRs++
					}
				}
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if config.debugMode {
		if totalPRs > 0 {
			fmt.Printf("  [Events] Complete: %d PRs found\n", totalPRs)
		} else {
			fmt.Println("  [Events] Complete: no new PRs found")
		}
	}
}

func collectSearchResults(query, label string, seenPRs *sync.Map, activitiesMap *sync.Map) {
	if config.localMode {
		if config.db == nil {
			return
		}

		allPRs, prLabels, err := config.db.GetAllPullRequestsWithLabels(config.debugMode)
		if err != nil {
			if config.debugMode {
				fmt.Printf("  [%s] Error loading from database: %v\n", label, err)
			}
			return
		}

		if config.debugMode {
			fmt.Printf("  [%s] Loading from database...\n", label)
		}

		totalFound := 0
		cutoffTime := time.Now().Add(-config.timeRange)
		for key, pr := range allPRs {
			storedLabel := prLabels[key]

			if storedLabel != label {
				continue
			}

			if pr.GetUpdatedAt().Time.Before(cutoffTime) {
				continue
			}

			parts := strings.Split(key, "/")
			if len(parts) < 2 {
				continue
			}
			owner := parts[0]
			repoAndNum := parts[1]
			repoParts := strings.Split(repoAndNum, "#")
			if len(repoParts) < 2 {
				continue
			}
			repo := repoParts[0]

			if !isRepoAllowed(owner, repo) {
				continue
			}

			prKey := key

			// Check if we've already processed this PR in activitiesMap
			existingActivity, alreadyProcessed := activitiesMap.Load(prKey)
			shouldProcess := true

			if alreadyProcessed {
				// PR is already in activitiesMap, check if we need to update the label
				existingPR := existingActivity.(*PRActivity)
				if shouldUpdateLabel(existingPR.Label, label, true) {
					// New label has higher priority, we'll update it
					if config.debugMode {
						fmt.Printf("  [%s] Updating label for %s from %s to %s (higher priority)\n", label, prKey, existingPR.Label, label)
					}
				} else {
					// Existing label has higher or equal priority, skip
					shouldProcess = false
				}
			}

			if shouldProcess {
				activity := PRActivity{
					Label:     label,
					Owner:     owner,
					Repo:      repo,
					PR:        pr,
					UpdatedAt: pr.GetUpdatedAt().Time,
				}
				activitiesMap.Store(prKey, &activity)
				totalFound++
			}
		}

		if config.debugMode && totalFound > 0 {
			fmt.Printf("  [%s] Complete: %d PRs found\n", label, totalFound)
		}

		return
	}

	opts := &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	totalFound := 0

	page := 1
	for {
		if config.debugMode {
			fmt.Printf("  [%s] Searching page %d with query: %s\n", label, page, query)
		}

		var result *github.IssuesSearchResult
		var resp *github.Response
		var err error

		retryErr := retryWithBackoff(func() error {
			result, resp, err = config.client.Search.Issues(config.ctx, query, opts)
			return err
		}, fmt.Sprintf("%s-page%d", label, page))

		config.progress.increment()
		if !config.debugMode {
			config.progress.display()
		}

		if page == 1 && resp != nil && resp.NextPage != 0 {
			lastPage := resp.LastPage
			if lastPage > 1 {
				additionalPages := lastPage - 1
				config.progress.addToTotal(additionalPages)
				if !config.debugMode {
					config.progress.display()
				}
			}
		}

		if retryErr != nil {
			fmt.Printf("  [%s] Error searching after retries: %v\n", label, retryErr)
			if resp != nil {
				fmt.Printf("  [%s] Rate limit remaining: %d/%d\n", label, resp.Rate.Remaining, resp.Rate.Limit)
			}
			return
		}

		if config.debugMode && resp != nil {
			fmt.Printf("  [%s] API Response: %d results, Rate: %d/%d\n", label, len(result.Issues), resp.Rate.Remaining, resp.Rate.Limit)
		}

		pageResults := 0
		for _, issue := range result.Issues {
			if issue.PullRequestLinks == nil {
				continue
			}

			repoURL := *issue.RepositoryURL
			parts := strings.Split(repoURL, "/")
			if len(parts) < 2 {
				fmt.Printf("  [%s] Error: Invalid repository URL format: %s\n", label, repoURL)
				continue
			}
			owner := parts[len(parts)-2]
			repo := parts[len(parts)-1]

			if !isRepoAllowed(owner, repo) {
				continue
			}

			prKey := fmt.Sprintf("%s/%s#%d", owner, repo, *issue.Number)

			// Check if we've already processed this PR in activitiesMap
			existingActivity, alreadyProcessed := activitiesMap.Load(prKey)
			shouldProcess := true

			if alreadyProcessed {
				// PR is already in activitiesMap, check if we need to update the label
				existingPR := existingActivity.(*PRActivity)
				if shouldUpdateLabel(existingPR.Label, label, true) {
					// New label has higher priority, we'll update it
					if config.debugMode {
						fmt.Printf("  [%s] Updating label for %s from %s to %s (higher priority)\n", label, prKey, existingPR.Label, label)
					}
				} else {
					// Existing label has higher or equal priority, skip
					shouldProcess = false
				}
			}

			if shouldProcess {
				// Store in seenPRs to prevent other goroutines from fetching the same PR
				seenPRs.Store(prKey, label)
				config.progress.addToTotal(1)
				if !config.debugMode {
					config.progress.display()
				}

				var pr *github.PullRequest
				var prErr error

				retryErr := retryWithBackoff(func() error {
					pr, _, prErr = config.client.PullRequests.Get(config.ctx, owner, repo, *issue.Number)
					return prErr
				}, fmt.Sprintf("%s-PR#%d", label, *issue.Number))

				config.progress.increment()
				if !config.debugMode {
					config.progress.display()
				}

				if retryErr != nil {
					fmt.Printf("  [%s] Warning: Could not fetch details for %s/%s#%d: %v\n", label, owner, repo, *issue.Number, retryErr)

					pr = &github.PullRequest{
						Number:    issue.Number,
						Title:     issue.Title,
						State:     issue.State,
						UpdatedAt: issue.UpdatedAt,
						User:      issue.User,
						HTMLURL:   issue.HTMLURL,
					}
				}

				hasUpdates := false

				if config.db != nil {
					cachedPR, err := config.db.GetPullRequest(owner, repo, *issue.Number)
					if err == nil {
						if pr.GetUpdatedAt().After(cachedPR.GetUpdatedAt().Time) {
							hasUpdates = true
							if config.debugMode {
								fmt.Printf("  [%s] Update detected: %s/%s#%d (API: %s > DB: %s)\n",
									label, owner, repo, *issue.Number,
									pr.GetUpdatedAt().Format("2006-01-02 15:04:05"),
									cachedPR.GetUpdatedAt().Time.Format("2006-01-02 15:04:05"))
							}
						}
					}
					_ = config.db.SavePullRequestWithLabel(owner, repo, pr, label, config.debugMode)
				}

				activity := PRActivity{
					Label:      label,
					Owner:      owner,
					Repo:       repo,
					PR:         pr,
					UpdatedAt:  pr.GetUpdatedAt().Time,
					HasUpdates: hasUpdates,
				}
				activitiesMap.Store(prKey, &activity)
				pageResults++
				totalFound++
			}
		}

		if config.debugMode {
			fmt.Printf("  [%s] Page %d: found %d new PRs (total: %d)\n", label, page, pageResults, totalFound)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
		page++
	}

	if config.debugMode && totalFound > 0 {
		fmt.Printf("  [%s] Complete: %d PRs found\n", label, totalFound)
	}
}

func displayPR(label, owner, repo string, pr *github.PullRequest, hasUpdates bool) {
	dateStr := "          "
	if pr.UpdatedAt != nil {
		dateStr = pr.UpdatedAt.Format("2006/01/02")
	}

	labelColor := getLabelColor(label)
	userColor := getUserColor(pr.User.GetLogin())

	updateIcon := ""
	if hasUpdates {
		updateIcon = color.New(color.FgYellow, color.Bold).Sprint("● ")
	}

	fmt.Printf("%s%s %s %s %s/%s#%d - %s\n",
		updateIcon,
		dateStr,
		labelColor.Sprint(strings.ToUpper(label)),
		userColor.Sprint(pr.User.GetLogin()),
		owner, repo, *pr.Number,
		*pr.Title,
	)

	if config.showLinks && pr.HTMLURL != nil {
		fmt.Printf("   🔗 %s\n", *pr.HTMLURL)
	}
}

func displayIssue(label, owner, repo string, issue *github.Issue, indented bool, hasUpdates bool) {
	dateStr := "          "
	if issue.UpdatedAt != nil {
		dateStr = issue.UpdatedAt.Format("2006/01/02")
	}

	indent := ""
	linkIndent := "   "
	if indented {
		state := strings.ToUpper(*issue.State)
		stateColor := getStateColor(*issue.State)
		indent = fmt.Sprintf("-- %s ", stateColor.Sprint(state))
		linkIndent = "      "
	}

	labelColor := getLabelColor(label)
	userColor := getUserColor(issue.User.GetLogin())

	updateIcon := ""
	if hasUpdates {
		updateIcon = color.New(color.FgYellow, color.Bold).Sprint("● ")
	}

	fmt.Printf("%s%s%s %s %s %s/%s#%d - %s\n",
		updateIcon,
		indent,
		dateStr,
		labelColor.Sprint(strings.ToUpper(label)),
		userColor.Sprint(issue.User.GetLogin()),
		owner, repo, *issue.Number,
		*issue.Title,
	)

	if config.showLinks && issue.HTMLURL != nil {
		fmt.Printf("%s🔗 %s\n", linkIndent, *issue.HTMLURL)
	}
}

func collectIssueSearchResults(query, label string, seenIssues *sync.Map, issueActivitiesMap *sync.Map) {
	if config.localMode {
		if config.db == nil {
			return
		}

		allIssues, issueLabels, err := config.db.GetAllIssuesWithLabels(config.debugMode)
		if err != nil {
			if config.debugMode {
				fmt.Printf("  [%s] Error loading from database: %v\n", label, err)
			}
			return
		}

		if config.debugMode {
			fmt.Printf("  [%s] Loading from database...\n", label)
		}

		totalFound := 0
		cutoffTime := time.Now().Add(-config.timeRange)
		for key, issue := range allIssues {
			storedLabel := issueLabels[key]

			if storedLabel != label {
				continue
			}

			if issue.GetUpdatedAt().Time.Before(cutoffTime) {
				continue
			}

			parts := strings.Split(key, "/")
			if len(parts) < 2 {
				continue
			}
			owner := parts[0]
			repoAndNum := parts[1]
			repoParts := strings.Split(repoAndNum, "#")
			if len(repoParts) < 2 {
				continue
			}
			repo := repoParts[0]

			if !isRepoAllowed(owner, repo) {
				continue
			}

			issueKey := key

			// Check if we've already processed this issue in issueActivitiesMap
			existingActivity, alreadyProcessed := issueActivitiesMap.Load(issueKey)
			shouldProcess := true

			if alreadyProcessed {
				// Issue is already in issueActivitiesMap, check if we need to update the label
				existingIssue := existingActivity.(*IssueActivity)
				if shouldUpdateLabel(existingIssue.Label, label, false) {
					// New label has higher priority, we'll update it
					if config.debugMode {
						fmt.Printf("  [%s] Updating label for %s from %s to %s (higher priority)\n", label, issueKey, existingIssue.Label, label)
					}
				} else {
					// Existing label has higher or equal priority, skip
					shouldProcess = false
				}
			}

			if shouldProcess {
				activity := IssueActivity{
					Label:     label,
					Owner:     owner,
					Repo:      repo,
					Issue:     issue,
					UpdatedAt: issue.GetUpdatedAt().Time,
				}
				issueActivitiesMap.Store(issueKey, &activity)
				totalFound++
			}
		}

		if config.debugMode && totalFound > 0 {
			fmt.Printf("  [%s] Complete: %d issues found\n", label, totalFound)
		}

		return
	}

	opts := &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}

	totalFound := 0

	page := 1
	for {
		if config.debugMode {
			fmt.Printf("  [%s] Searching page %d with query: %s\n", label, page, query)
		}

		var result *github.IssuesSearchResult
		var resp *github.Response
		var err error

		retryErr := retryWithBackoff(func() error {
			result, resp, err = config.client.Search.Issues(config.ctx, query, opts)
			return err
		}, fmt.Sprintf("%s-issues-page%d", label, page))

		config.progress.increment()
		if !config.debugMode {
			config.progress.display()
		}

		if page == 1 && resp != nil && resp.NextPage != 0 {
			lastPage := resp.LastPage
			if lastPage > 1 {
				additionalPages := lastPage - 1
				config.progress.addToTotal(additionalPages)
				if !config.debugMode {
					config.progress.display()
				}
			}
		}

		if retryErr != nil {
			fmt.Printf("  [%s] Error searching after retries: %v\n", label, retryErr)
			if resp != nil {
				fmt.Printf("  [%s] Rate limit remaining: %d/%d\n", label, resp.Rate.Remaining, resp.Rate.Limit)
			}
			return
		}

		if config.debugMode && resp != nil {
			fmt.Printf("  [%s] API Response: %d results, Rate: %d/%d\n", label, len(result.Issues), resp.Rate.Remaining, resp.Rate.Limit)
		}

		pageResults := 0
		for _, issue := range result.Issues {
			if issue.PullRequestLinks != nil {
				continue
			}

			repoURL := *issue.RepositoryURL
			parts := strings.Split(repoURL, "/")
			if len(parts) < 2 {
				fmt.Printf("  [%s] Error: Invalid repository URL format: %s\n", label, repoURL)
				continue
			}
			owner := parts[len(parts)-2]
			repo := parts[len(parts)-1]

			if !isRepoAllowed(owner, repo) {
				continue
			}

			issueKey := fmt.Sprintf("%s/%s#%d", owner, repo, *issue.Number)

			// Check if we've already processed this issue in issueActivitiesMap
			existingActivity, alreadyProcessed := issueActivitiesMap.Load(issueKey)
			shouldProcess := true

			if alreadyProcessed {
				// Issue is already in issueActivitiesMap, check if we need to update the label
				existingIssue := existingActivity.(*IssueActivity)
				if shouldUpdateLabel(existingIssue.Label, label, false) {
					// New label has higher priority, we'll update it
					if config.debugMode {
						fmt.Printf("  [%s] Updating label for %s from %s to %s (higher priority)\n", label, issueKey, existingIssue.Label, label)
					}
				} else {
					// Existing label has higher or equal priority, skip
					shouldProcess = false
				}
			}

			if shouldProcess {
				hasUpdates := false

				if config.db != nil {
					cachedIssue, err := config.db.GetIssue(owner, repo, *issue.Number)
					if err == nil {
						if issue.GetUpdatedAt().After(cachedIssue.GetUpdatedAt().Time) {
							hasUpdates = true
						}
					}
					_ = config.db.SaveIssueWithLabel(owner, repo, issue, label, config.debugMode)
				}

				activity := IssueActivity{
					Label:      label,
					Owner:      owner,
					Repo:       repo,
					Issue:      issue,
					UpdatedAt:  issue.GetUpdatedAt().Time,
					HasUpdates: hasUpdates,
				}
				issueActivitiesMap.Store(issueKey, &activity)
				pageResults++
				totalFound++
			}
		}

		if config.debugMode {
			fmt.Printf("  [%s] Page %d: found %d new issues (total: %d)\n", label, page, pageResults, totalFound)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
		page++
	}

	if config.debugMode && totalFound > 0 {
		fmt.Printf("  [%s] Complete: %d issues found\n", label, totalFound)
	}
}
