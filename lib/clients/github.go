package clients

import (
	"context"
	"fmt"

	"time"

	"github.com/cenkalti/backoff"
	"github.com/coreos/issue-sync/cfg"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// GitHubClient is a wrapper around the GitHub API Client library we
// use. It allows us to swap in other implementations, such as a dry run
// clients, or mock clients for testing.
type GitHubClient interface {
	ListIssues() ([]github.Issue, error)
	ListComments(issue github.Issue) ([]*github.IssueComment, error)
	GetUser(login string) (github.User, error)
	GetRateLimits() (github.RateLimits, error)
}

// realGHClient is a standard GitHub clients, that actually makes all of the
// requests against the GitHub REST API. It is the canonical implementation
// of GitHubClient.
type realGHClient struct {
	config cfg.Config
	client github.Client
}

// ListIssues returns the list of GitHub issues since the last run of the tool.
func (g realGHClient) ListIssues() ([]github.Issue, error) {
	log := g.config.GetLogger()

	ctx := context.Background()

	user, repo := g.config.GetRepo()

	// Set it so that it will run the loop once, and it'll be updated in the loop.
	pages := 1
	var issues []github.Issue

	for page := 0; page < pages; page++ {
		is, res, err := g.request(func() (interface{}, *github.Response, error) {
			return g.client.Issues.ListByRepo(ctx, user, repo, &github.IssueListByRepoOptions{
				Since:     g.config.GetSinceParam(),
				State:     "all",
				Sort:      "created",
				Direction: "asc",
				ListOptions: github.ListOptions{
					Page:    page,
					PerPage: 100,
				},
			})
		})
		if err != nil {
			return nil, err
		}
		issuePointers, ok := is.([]*github.Issue)
		if !ok {
			log.Errorf("Get GitHub issues did not return issues! Got: %v", is)
			return nil, fmt.Errorf("get GitHub issues failed: expected []*github.Issue; got %T", is)
		}

		var issuePage []github.Issue
		for _, v := range issuePointers {
			// If PullRequestLinks is not nil, it's a Pull Request
			if v.PullRequestLinks == nil {
				issuePage = append(issuePage, *v)
			}
		}

		pages = res.LastPage
		issues = append(issues, issuePage...)
	}

	log.Debug("Collected all GitHub issues")

	return issues, nil
}

// ListComments returns the list of all comments on a GitHub issue in
// ascending order of creation.
func (g realGHClient) ListComments(issue github.Issue) ([]*github.IssueComment, error) {
	log := g.config.GetLogger()

	ctx := context.Background()
	user, repo := g.config.GetRepo()
	c, _, err := g.request(func() (interface{}, *github.Response, error) {
		return g.client.Issues.ListComments(ctx, user, repo, issue.GetNumber(), &github.IssueListCommentsOptions{
			Sort:      "created",
			Direction: "asc",
		})
	})
	if err != nil {
		log.Errorf("Error retrieving GitHub comments for issue #%d. Error: %v.", issue.GetNumber(), err)
		return nil, err
	}
	comments, ok := c.([]*github.IssueComment)
	if !ok {
		log.Errorf("Get GitHub comments did not return comments! Got: %v", c)
		return nil, fmt.Errorf("Get GitHub comments failed: expected []*github.IssueComment; got %T", c)
	}

	return comments, nil
}

// GetUser returns a GitHub user from its login.
func (g realGHClient) GetUser(login string) (github.User, error) {
	log := g.config.GetLogger()

	u, _, err := g.request(func() (interface{}, *github.Response, error) {
		return g.client.Users.Get(context.Background(), login)
	})

	if err != nil {
		log.Errorf("Error retrieving GitHub user %s. Error: %v", login, err)
	}

	user, ok := u.(*github.User)
	if !ok {
		log.Errorf("Get GitHub user did not return user! Got: %v", u)
		return github.User{}, fmt.Errorf("Get GitHub user failed: expected *github.User; got %T", u)
	}

	return *user, nil
}

// GetRateLimits returns the current rate limits on the GitHub API. This is a
// simple and lightweight request that can also be used simply for testing the API.
func (g realGHClient) GetRateLimits() (github.RateLimits, error) {
	log := g.config.GetLogger()

	ctx := context.Background()

	rl, _, err := g.request(func() (interface{}, *github.Response, error) {
		return g.client.RateLimits(ctx)
	})
	if err != nil {
		log.Errorf("Error connecting to GitHub; check your token. Error: %v", err)
		return github.RateLimits{}, err
	}
	rate, ok := rl.(*github.RateLimits)
	if !ok {
		log.Errorf("Get GitHub rate limits did not return rate limits! Got: %v", rl)
		return github.RateLimits{}, fmt.Errorf("Get GitHub rate limits failed: expected *github.RateLimits; got %T", rl)
	}

	return *rate, nil
}

const retryBackoffRoundRatio = time.Millisecond / time.Nanosecond

// request takes an API function from the GitHub library
// and calls it with exponential backoff. If the function succeeds, it
// returns the expected value and the GitHub API response, as well as a nil
// error. If it continues to fail until a maximum time is reached, it returns
// a nil result as well as the returned HTTP response and a timeout error.
func (g realGHClient) request(f func() (interface{}, *github.Response, error)) (interface{}, *github.Response, error) {
	log := g.config.GetLogger()

	var ret interface{}
	var res *github.Response
	var err error

	op := func() error {
		ret, res, err = f()
		return err
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = g.config.GetTimeout()

	er := backoff.RetryNotify(op, b, func(err error, duration time.Duration) {
		// Round to a whole number of milliseconds
		duration /= retryBackoffRoundRatio // Convert nanoseconds to milliseconds
		duration *= retryBackoffRoundRatio // Convert back so it appears correct

		log.Errorf("Error performing operation; retrying in %v: %v", duration, err)
	})
	if er != nil {
		return nil, nil, er
	}

	return ret, res, err
}

// NewGitHubClient creates a GitHubClient and returns it; which
// implementation it uses depends on the configuration of this
// run. For example, a dry-run clients may be created which does
// not make any requests that would change anything on the server,
// but instead simply prints out the actions that it's asked to take.
func NewGitHubClient(config cfg.Config) (GitHubClient, error) {
	var ret GitHubClient

	log := config.GetLogger()

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: config.GetConfigString("github-token")},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	ret = realGHClient{
		config: config,
		client: *client,
	}

	// Make a request so we can check that we can connect fine.
	_, err := ret.GetRateLimits()
	if err != nil {
		return realGHClient{}, err
	}
	log.Debug("Successfully connected to GitHub.")

	return ret, nil
}
