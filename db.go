package main

import (
	"encoding/json"
	"fmt"
	"os"
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

type Database struct {
	db *bolt.DB
}

// buildItemKey creates a consistent key format for PRs and issues
func buildItemKey(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

// buildCommentKey creates a consistent key format for comments
func buildCommentKey(owner, repo string, itemNumber int, commentType string, commentID int64) string {
	return fmt.Sprintf("%s/%s#%d/%s/%d", owner, repo, itemNumber, commentType, commentID)
}

// save is a generic function to save data to a bucket with consistent error handling and logging
func (d *Database) save(bucket []byte, key string, data interface{}, debugMode bool, itemType string) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error marshaling %s %s: %v\n", itemType, key, err)
		}
		return fmt.Errorf("failed to marshal %s: %w", itemType, err)
	}

	err = d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		return b.Put([]byte(key), jsonData)
	})

	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error saving %s %s: %v\n", itemType, key, err)
		}
	} else if debugMode {
		fmt.Printf("  [DB] Saved %s %s\n", itemType, key)
	}

	return err
}

func OpenDatabase(path string) (*Database, error) {
	db, err := bolt.Open(path, 0666, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := os.Chmod(path, 0666); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set database permissions: %w", err)
	}

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

func (d *Database) Close() error {
	return d.db.Close()
}

type PRWithLabel struct {
	PR    *github.PullRequest
	Label string
}

func (d *Database) SavePullRequest(owner, repo string, pr *github.PullRequest, debugMode bool) error {
	key := buildItemKey(owner, repo, pr.GetNumber())
	return d.save(pullRequestsBucket, key, pr, debugMode, "PR")
}

func (d *Database) SavePullRequestWithLabel(owner, repo string, pr *github.PullRequest, label string, debugMode bool) error {
	key := buildItemKey(owner, repo, pr.GetNumber())
	prWithLabel := PRWithLabel{
		PR:    pr,
		Label: label,
	}
	return d.save(pullRequestsBucket, key, prWithLabel, debugMode, fmt.Sprintf("PR with label %s", label))
}

func (d *Database) GetPullRequest(owner, repo string, number int) (*github.PullRequest, error) {
	key := buildItemKey(owner, repo, number)

	var pr github.PullRequest
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(pullRequestsBucket)
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("PR not found")
		}

		var prWithLabel PRWithLabel
		if err := json.Unmarshal(data, &prWithLabel); err == nil && prWithLabel.PR != nil {
			pr = *prWithLabel.PR
			return nil
		}

		return json.Unmarshal(data, &pr)
	})

	if err != nil {
		return nil, err
	}
	return &pr, nil
}

func (d *Database) GetPullRequestWithLabel(owner, repo string, number int) (*github.PullRequest, string, error) {
	key := buildItemKey(owner, repo, number)

	var pr *github.PullRequest
	var label string

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(pullRequestsBucket)
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("PR not found")
		}

		var prWithLabel PRWithLabel
		if err := json.Unmarshal(data, &prWithLabel); err == nil && prWithLabel.PR != nil {
			pr = prWithLabel.PR
			label = prWithLabel.Label
			return nil
		}

		var oldPR github.PullRequest
		if err := json.Unmarshal(data, &oldPR); err != nil {
			return err
		}
		pr = &oldPR
		label = ""
		return nil
	})

	if err != nil {
		return nil, "", err
	}
	return pr, label, nil
}

type IssueWithLabel struct {
	Issue *github.Issue
	Label string
}

func (d *Database) SaveIssue(owner, repo string, issue *github.Issue, debugMode bool) error {
	key := buildItemKey(owner, repo, issue.GetNumber())
	return d.save(issuesBucket, key, issue, debugMode, "issue")
}

func (d *Database) SaveIssueWithLabel(owner, repo string, issue *github.Issue, label string, debugMode bool) error {
	key := buildItemKey(owner, repo, issue.GetNumber())
	issueWithLabel := IssueWithLabel{
		Issue: issue,
		Label: label,
	}
	return d.save(issuesBucket, key, issueWithLabel, debugMode, fmt.Sprintf("issue with label %s", label))
}

func (d *Database) GetIssue(owner, repo string, number int) (*github.Issue, error) {
	key := buildItemKey(owner, repo, number)

	var issue github.Issue
	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(issuesBucket)
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("issue not found")
		}

		var issueWithLabel IssueWithLabel
		if err := json.Unmarshal(data, &issueWithLabel); err == nil && issueWithLabel.Issue != nil {
			issue = *issueWithLabel.Issue
			return nil
		}

		return json.Unmarshal(data, &issue)
	})

	if err != nil {
		return nil, err
	}
	return &issue, nil
}

func (d *Database) GetIssueWithLabel(owner, repo string, number int) (*github.Issue, string, error) {
	key := buildItemKey(owner, repo, number)

	var issue *github.Issue
	var label string

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(issuesBucket)
		data := b.Get([]byte(key))
		if data == nil {
			return fmt.Errorf("issue not found")
		}

		var issueWithLabel IssueWithLabel
		if err := json.Unmarshal(data, &issueWithLabel); err == nil && issueWithLabel.Issue != nil {
			issue = issueWithLabel.Issue
			label = issueWithLabel.Label
			return nil
		}

		var oldIssue github.Issue
		if err := json.Unmarshal(data, &oldIssue); err != nil {
			return err
		}
		issue = &oldIssue
		label = ""
		return nil
	})

	if err != nil {
		return nil, "", err
	}
	return issue, label, nil
}

func (d *Database) SaveComment(owner, repo string, itemNumber int, comment *github.IssueComment, commentType string) error {
	key := buildCommentKey(owner, repo, itemNumber, commentType, comment.GetID())

	data, err := json.Marshal(comment)
	if err != nil {
		return fmt.Errorf("failed to marshal comment: %w", err)
	}

	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(commentsBucket)
		return b.Put([]byte(key), data)
	})
}

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

func (d *Database) GetComment(owner, repo string, itemNumber int, commentType string, commentID int64) (*github.IssueComment, error) {
	key := buildCommentKey(owner, repo, itemNumber, commentType, commentID)

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

func (d *Database) Stats() (prCount, issueCount, commentCount int, err error) {
	err = d.db.View(func(tx *bolt.Tx) error {
		prCount = tx.Bucket(pullRequestsBucket).Stats().KeyN
		issueCount = tx.Bucket(issuesBucket).Stats().KeyN
		commentCount = tx.Bucket(commentsBucket).Stats().KeyN
		return nil
	})
	return
}

func (d *Database) GetAllPullRequests(debugMode bool) (map[string]*github.PullRequest, error) {
	prs := make(map[string]*github.PullRequest)

	if debugMode {
		fmt.Printf("  [DB] Reading all PRs from database...\n")
	}

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(pullRequestsBucket)
		return b.ForEach(func(k, v []byte) error {
			var prWithLabel PRWithLabel
			if err := json.Unmarshal(v, &prWithLabel); err == nil && prWithLabel.PR != nil {
				prs[string(k)] = prWithLabel.PR
				return nil
			}

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

func (d *Database) GetAllPullRequestsWithLabels(debugMode bool) (map[string]*github.PullRequest, map[string]string, error) {
	prs := make(map[string]*github.PullRequest)
	labels := make(map[string]string)

	if debugMode {
		fmt.Printf("  [DB] Reading all PRs with labels from database...\n")
	}

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(pullRequestsBucket)
		return b.ForEach(func(k, v []byte) error {
			key := string(k)

			var prWithLabel PRWithLabel
			if err := json.Unmarshal(v, &prWithLabel); err == nil && prWithLabel.PR != nil {
				prs[key] = prWithLabel.PR
				labels[key] = prWithLabel.Label
				return nil
			}

			var pr github.PullRequest
			if err := json.Unmarshal(v, &pr); err != nil {
				if debugMode {
					fmt.Printf("  [DB] Error unmarshaling PR %s: %v\n", key, err)
				}
				return err
			}
			prs[key] = &pr
			labels[key] = "" // No label in old format
			return nil
		})
	})

	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error reading PRs: %v\n", err)
		}
		return nil, nil, err
	}

	if debugMode {
		fmt.Printf("  [DB] Loaded %d PRs from database\n", len(prs))
	}

	return prs, labels, nil
}

func (d *Database) GetAllIssues(debugMode bool) (map[string]*github.Issue, error) {
	issues := make(map[string]*github.Issue)

	if debugMode {
		fmt.Printf("  [DB] Reading all issues from database...\n")
	}

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(issuesBucket)
		return b.ForEach(func(k, v []byte) error {
			var issueWithLabel IssueWithLabel
			if err := json.Unmarshal(v, &issueWithLabel); err == nil && issueWithLabel.Issue != nil {
				issues[string(k)] = issueWithLabel.Issue
				return nil
			}

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

func (d *Database) GetAllIssuesWithLabels(debugMode bool) (map[string]*github.Issue, map[string]string, error) {
	issues := make(map[string]*github.Issue)
	labels := make(map[string]string)

	if debugMode {
		fmt.Printf("  [DB] Reading all issues with labels from database...\n")
	}

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(issuesBucket)
		return b.ForEach(func(k, v []byte) error {
			key := string(k)

			var issueWithLabel IssueWithLabel
			if err := json.Unmarshal(v, &issueWithLabel); err == nil && issueWithLabel.Issue != nil {
				issues[key] = issueWithLabel.Issue
				labels[key] = issueWithLabel.Label
				return nil
			}

			var issue github.Issue
			if err := json.Unmarshal(v, &issue); err != nil {
				if debugMode {
					fmt.Printf("  [DB] Error unmarshaling issue %s: %v\n", key, err)
				}
				return err
			}
			issues[key] = &issue
			labels[key] = "" // No label in old format
			return nil
		})
	})

	if err != nil {
		if debugMode {
			fmt.Printf("  [DB] Error reading issues: %v\n", err)
		}
		return nil, nil, err
	}

	if debugMode {
		fmt.Printf("  [DB] Loaded %d issues from database\n", len(issues))
	}

	return issues, labels, nil
}

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

func (d *Database) GetPRComments(owner, repo string, prNumber int) ([]*github.PullRequestComment, error) {
	var comments []*github.PullRequestComment
	prefix := fmt.Sprintf("%s/%s#%d/pr_review_comment/", owner, repo, prNumber)

	err := d.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(commentsBucket)
		c := b.Cursor()

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
