package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v57/github"
	bolt "go.etcd.io/bbolt"
)

var (
	pullRequestsBucket = []byte("pull_requests")
	issuesBucket       = []byte("issues")
	commentsBucket     = []byte("comments")
)

// Database wraps bbolt database operations
type Database struct {
	db *bolt.DB
}

// OpenDatabase opens or creates the bbolt database
func OpenDatabase(path string) (*Database, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Create buckets if they don't exist
	err = db.Update(func(tx *bolt.Tx) error {
		buckets := [][]byte{pullRequestsBucket, issuesBucket, commentsBucket}
		for _, bucket := range buckets {
			_, err := tx.CreateBucketIfNotExists(bucket)
			if err != nil {
				return fmt.Errorf("failed to create bucket %s: %w", string(bucket), err)
			}
		}
		return nil
	})

	if err != nil {
		db.Close()
		return nil, err
	}

	return &Database{db: db}, nil
}

// Close closes the database
func (d *Database) Close() error {
	return d.db.Close()
}

// SavePullRequest saves or updates a pull request in the database
func (d *Database) SavePullRequest(owner, repo string, pr *github.PullRequest, debugMode bool) error {
	key := fmt.Sprintf("%s/%s#%d", owner, repo, pr.GetNumber())

	data, err := json.Marshal(pr)
	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error marshaling PR %s: %v\n", key, err)
		}
		return fmt.Errorf("failed to marshal PR: %w", err)
	}

	err = d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(pullRequestsBucket)
		return b.Put([]byte(key), data)
	})

	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error saving PR %s: %v\n", key, err)
		}
	} else if debugMode {
		fmt.Printf("  [DB] Saved PR %s\n", key)
	}

	return err
}

// GetPullRequest retrieves a pull request from the database
func (d *Database) GetPullRequest(owner, repo string, number int) (*github.PullRequest, error) {
	key := fmt.Sprintf("%s/%s#%d", owner, repo, number)

	var pr github.PullRequest
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(pullRequestsBucket)
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("PR not found")
		}
		return json.Unmarshal(data, &pr)
	})

	if err != nil {
		return nil, err
	}
	return &pr, nil
}

// SaveIssue saves or updates an issue in the database
func (d *Database) SaveIssue(owner, repo string, issue *github.Issue, debugMode bool) error {
	key := fmt.Sprintf("%s/%s#%d", owner, repo, issue.GetNumber())

	data, err := json.Marshal(issue)
	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error marshaling issue %s: %v\n", key, err)
		}
		return fmt.Errorf("failed to marshal issue: %w", err)
	}

	err = d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(issuesBucket)
		return b.Put([]byte(key), data)
	})

	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error saving issue %s: %v\n", key, err)
		}
	} else if debugMode {
		fmt.Printf("  [DB] Saved issue %s\n", key)
	}

	return err
}

// GetIssue retrieves an issue from the database
func (d *Database) GetIssue(owner, repo string, number int) (*github.Issue, error) {
	key := fmt.Sprintf("%s/%s#%d", owner, repo, number)

	var issue github.Issue
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(issuesBucket)
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("issue not found")
		}
		return json.Unmarshal(data, &issue)
	})

	if err != nil {
		return nil, err
	}
	return &issue, nil
}

// SaveComment saves or updates a comment in the database
// commentType should be "pr_comment" or "issue_comment"
func (d *Database) SaveComment(owner, repo string, itemNumber int, comment *github.IssueComment, commentType string) error {
	key := fmt.Sprintf("%s/%s#%d/%s/%d", owner, repo, itemNumber, commentType, comment.GetID())

	data, err := json.Marshal(comment)
	if err != nil {
		return fmt.Errorf("failed to marshal comment: %w", err)
	}

	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(commentsBucket)
		return b.Put([]byte(key), data)
	})
}

// SavePRComment saves or updates a PR review comment in the database
func (d *Database) SavePRComment(owner, repo string, prNumber int, comment *github.PullRequestComment, debugMode bool) error {
	key := fmt.Sprintf("%s/%s#%d/pr_review_comment/%d", owner, repo, prNumber, comment.GetID())

	data, err := json.Marshal(comment)
	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error marshaling PR comment %s: %v\n", key, err)
		}
		return fmt.Errorf("failed to marshal PR comment: %w", err)
	}

	err = d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(commentsBucket)
		return b.Put([]byte(key), data)
	})

	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error saving PR comment %s: %v\n", key, err)
		}
	} else if debugMode {
		fmt.Printf("  [DB] Saved PR comment %s\n", key)
	}

	return err
}

// GetComment retrieves a comment from the database
func (d *Database) GetComment(owner, repo string, itemNumber int, commentType string, commentID int64) (*github.IssueComment, error) {
	key := fmt.Sprintf("%s/%s#%d/%s/%d", owner, repo, itemNumber, commentType, commentID)

	var comment github.IssueComment
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(commentsBucket)
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("comment not found")
		}
		return json.Unmarshal(data, &comment)
	})

	if err != nil {
		return nil, err
	}
	return &comment, nil
}

// Stats returns database statistics
func (d *Database) Stats() (prCount, issueCount, commentCount int, err error) {
	err = d.db.View(func(tx *bolt.Tx) error {
		prCount = tx.Bucket(pullRequestsBucket).Stats().KeyN
		issueCount = tx.Bucket(issuesBucket).Stats().KeyN
		commentCount = tx.Bucket(commentsBucket).Stats().KeyN
		return nil
	})
	return
}

// GetAllPullRequests retrieves all pull requests from the database
func (d *Database) GetAllPullRequests(debugMode bool) (map[string]*github.PullRequest, error) {
	prs := make(map[string]*github.PullRequest)

	if debugMode {
		fmt.Printf("  [DB] Reading all PRs from database...\n")
	}

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(pullRequestsBucket)
		return b.ForEach(func(k, v []byte) error {
			var pr github.PullRequest
			if err := json.Unmarshal(v, &pr); err != nil {
				if debugMode {
					fmt.Printf("  [DB] Error unmarshaling PR %s: %v\n", string(k), err)
				}
				return err
			}
			prs[string(k)] = &pr
			return nil
		})
	})

	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error reading PRs: %v\n", err)
		}
		return nil, err
	}

	if debugMode {
		fmt.Printf("  [DB] Loaded %d PRs from database\n", len(prs))
	}

	return prs, nil
}

// GetAllIssues retrieves all issues from the database
func (d *Database) GetAllIssues(debugMode bool) (map[string]*github.Issue, error) {
	issues := make(map[string]*github.Issue)

	if debugMode {
		fmt.Printf("  [DB] Reading all issues from database...\n")
	}

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(issuesBucket)
		return b.ForEach(func(k, v []byte) error {
			var issue github.Issue
			if err := json.Unmarshal(v, &issue); err != nil {
				if debugMode {
					fmt.Printf("  [DB] Error unmarshaling issue %s: %v\n", string(k), err)
				}
				return err
			}
			issues[string(k)] = &issue
			return nil
		})
	})

	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error reading issues: %v\n", err)
		}
		return nil, err
	}

	if debugMode {
		fmt.Printf("  [DB] Loaded %d issues from database\n", len(issues))
	}

	return issues, nil
}

// GetAllComments retrieves all comments from the database
func (d *Database) GetAllComments() ([]string, error) {
	var comments []string

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(commentsBucket)
		return b.ForEach(func(k, v []byte) error {
			comments = append(comments, string(v))
			return nil
		})
	})

	if err != nil {
		return nil, err
	}
	return comments, nil
}

// GetPRComments retrieves all PR review comments for a specific PR from the database
func (d *Database) GetPRComments(owner, repo string, prNumber int) ([]*github.PullRequestComment, error) {
	var comments []*github.PullRequestComment
	prefix := fmt.Sprintf("%s/%s#%d/pr_review_comment/", owner, repo, prNumber)

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(commentsBucket)
		c := b.Cursor()

		// Seek to the prefix and iterate over matching keys
		for k, v := c.Seek([]byte(prefix)); k != nil && strings.HasPrefix(string(k), prefix); k, v = c.Next() {
			var comment github.PullRequestComment
			if err := json.Unmarshal(v, &comment); err != nil {
				return err
			}
			comments = append(comments, &comment)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return comments, nil
}
