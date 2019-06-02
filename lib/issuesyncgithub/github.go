package issuesyncgithub

import (
	"context"
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/coreos/issue-sync/lib/models"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/coreos/issue-sync/cfg"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// Client is a wrapper around the GitHub API Client library we
// use. It allows us to swap in other implementations, such as a dry run
// clients, or mock clients for testing.
type Client interface {
	listIssueEvents(ctx context.Context, owner, repo string, number int, page int) ([]*github.IssueEvent, *github.Response, error)
	listByRepo(ctx context.Context, owner string, repo string, page int, since time.Time) ([]*github.Issue, *github.Response, error)
	getRepository(ctx context.Context, owner string, repo string) (*github.Repository, *github.Response, error)
	listComments(ctx context.Context, owner string, repo string, number int) ([]*github.IssueComment, *github.Response, error)
	getUser(ctx context.Context, user string) (*github.User, *github.Response, error)
	getRateLimits(ctx context.Context) (*github.RateLimits, *github.Response, error)
}

// realGHClient is a standard GitHub clients, that actually makes all of the
// requests against the GitHub REST API. It is the canonical implementation
// of Client.
type realGHClient struct {
	client github.Client
}

func (g realGHClient) listIssueEvents(ctx context.Context, owner, repo string, number int, page int)  ([]*github.IssueEvent, *github.Response, error) {
	return g.client.Issues.ListIssueEvents(ctx, owner, repo, number, &github.ListOptions{
		Page:    page,
		PerPage: 100,
	})
}

func (g realGHClient) getRepository(ctx context.Context, owner string, repo string) (*github.Repository, *github.Response, error) {
	return g.client.Repositories.Get(ctx, owner, repo)
}

func (g realGHClient) listByRepo(ctx context.Context, owner string, repo string, page int, since time.Time) ([]*github.Issue, *github.Response, error) {
	return g.client.Issues.ListByRepo(ctx, owner, repo, &github.IssueListByRepoOptions{
		Since:     since,
		State:     "all",
		Sort:      "created",
		Direction: "asc",
		ListOptions: github.ListOptions{
			Page:    page,
			PerPage: 100,
		},
	})
}

func (g realGHClient) listComments(ctx context.Context, owner string, repo string, number int) ([]*github.IssueComment, *github.Response, error) {
	return g.client.Issues.ListComments(ctx, owner, repo, number, &github.IssueListCommentsOptions{
		Sort:      "created",
		Direction: "asc",
	})
}

func (g realGHClient) getUser(ctx context.Context, user string) (*github.User, *github.Response, error) {
	return g.client.Users.Get(context.Background(), user)
}

func (g realGHClient) getRateLimits(ctx context.Context) (*github.RateLimits, *github.Response, error) {
	return g.client.RateLimits(ctx)
}

type TestGHClient struct {
	handleListIssueEvents func(ctx context.Context, owner, repo string, number int, page int) ([]*github.IssueEvent, *github.Response, error)
	handleListByRepo func(ctx context.Context, owner string, repo string, page int, since time.Time) ([]*github.Issue, *github.Response, error)
	handleGetRepository func(ctx context.Context, owner string, repo string) (*github.Repository, *github.Response, error)
	handleListComments func(ctx context.Context, owner string, repo string, number int) ([]*github.IssueComment, *github.Response, error)
	handleGetUser func(ctx context.Context, user string) (*github.User, *github.Response, error)
	handleGetRateLimits func(ctx context.Context) (*github.RateLimits, *github.Response, error)
}

func (g TestGHClient) listIssueEvents(ctx context.Context, owner, repo string, number int, page int) ([]*github.IssueEvent, *github.Response, error) {
	return g.handleListIssueEvents(ctx, owner, repo, number, page)
}

func (g TestGHClient) getRepository(ctx context.Context, owner string, repo string) (*github.Repository, *github.Response, error) {
	return g.handleGetRepository(ctx, owner, repo)
}

func (g TestGHClient) listByRepo(ctx context.Context, owner string, repo string, page int, since time.Time) ([]*github.Issue, *github.Response, error) {
	return g.handleListByRepo(ctx, owner, repo, page, since)
}

func (g TestGHClient) listComments(ctx context.Context, owner string, repo string, number int) ([]*github.IssueComment, *github.Response, error) {
	return g.handleListComments(ctx, owner, repo, number)
}

func (g TestGHClient) getUser(ctx context.Context, user string) (*github.User, *github.Response, error) {
	return g.handleGetUser(ctx, user)
}

func (g TestGHClient) getRateLimits(ctx context.Context) (*github.RateLimits, *github.Response, error) {
	return g.getRateLimits(ctx)
}

func getCurrentProjectCardAndCommitIds(g Client, log logrus.Entry, timeout time.Duration, user string, repoName string, issue *github.Issue) (*github.ProjectCard, []string, error) {
	ctx := context.Background()
	pages := 1

	var currentProjectCard *github.ProjectCard

	var commitIds []string

	// search for the current project card
	for page := 1; page <= pages; page++ {
		is, res, err := request(log, timeout, func() (interface{}, *github.Response, error) {
			return g.listIssueEvents(ctx, user, repoName, issue.GetNumber(), page)
		})
		if err != nil {
			return nil, nil, err
		}
		issueEventPointers, ok := is.([]*github.IssueEvent)
		if !ok {
			log.Errorf("Get GitHub issue events did not return issue events! Got: %v", is)
			return nil, nil, fmt.Errorf("get GitHub issues events failed: expected []*github.IssueEvent; got %T", is)
		}

		for _, v := range issueEventPointers {
			if v.ProjectCard != nil {
				if v.GetEvent() == "added_to_project" || v.GetEvent() == "moved_columns_in_project" {
					currentProjectCard = v.ProjectCard
				} else if *v.Event == "removed_from_project" {
					currentProjectCard = nil
				}
			}

			if v.CommitID != nil {
				commitIds = append(commitIds, *v.CommitID)
			}
		}
		pages = res.LastPage
	}

	log.Debugf("Found current Project Card for issue #%d", issue.GetNumber())

	return currentProjectCard, commitIds, nil
}

// ListIssues returns the list of GitHub issues since the last run of the tool.
func ListIssues(g Client, log logrus.Entry, timeout time.Duration, user string, repoName string, since time.Time) ([]models.ExtendedGithubIssue, error) {
	ctx := context.Background()

	repo, _, _ := g.getRepository(ctx, user, repoName)

	// Set it so that it will run the loop once, and it'll be updated in the loop.
	pages := 1
	var issues []models.ExtendedGithubIssue

	for page := 1; page <= pages; page++ {
		is, res, err := request(log, timeout, func() (interface{}, *github.Response, error) {
			return g.listByRepo(ctx, user, repoName, page, since)
		})
		if err != nil {
			return nil, err
		}
		issuePointers, ok := is.([]*github.Issue)
		if !ok {
			log.Errorf("Get GitHub issues did not return issues! Got: %v", is)
			return nil, fmt.Errorf("get GitHub issues failed: expected []*github.Issue; got %T", is)
		}

		var issuePage []models.ExtendedGithubIssue
		for _, v := range issuePointers {
			// If PullRequestLinks is not nil, it's a Pull Request
			if v.PullRequestLinks == nil {

				var currentProjectCard *github.ProjectCard
				var commitIds []string
				if repo.GetHasProjects() {
					currentProjectCard, commitIds, _ = getCurrentProjectCardAndCommitIds(g, log, timeout, user, repoName, v)
				}

				issuePage = append(issuePage, models.ExtendedGithubIssue{Issue: *v, ProjectCard: currentProjectCard, CommitIds: commitIds})
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
func ListComments(g Client, log logrus.Entry, timeout time.Duration, user string, repoName string, issue github.Issue) ([]*github.IssueComment, error) {

	ctx := context.Background()
	c, _, err := request(log, timeout, func() (interface{}, *github.Response, error) {
		return g.listComments(ctx, user, repoName, issue.GetNumber())
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
func GetUser(g Client, log logrus.Entry, timeout time.Duration, userName string) (github.User, error) {
	u, _, err := request(log, timeout, func() (interface{}, *github.Response, error) {
		return g.getUser(context.Background(), userName)
	})

	if err != nil {
		log.Errorf("Error retrieving GitHub user %s. Error: %v", userName, err)
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
func GetRateLimits(g Client, log logrus.Entry, timeout time.Duration) (github.RateLimits, error) {
	ctx := context.Background()

	rl, _, err := request(log, timeout, func() (interface{}, *github.Response, error) {
		return g.getRateLimits(ctx)
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
func request(log logrus.Entry, timeout time.Duration, f func() (interface{}, *github.Response, error)) (interface{}, *github.Response, error) {

	var ret interface{}
	var res *github.Response

	op := func() error {
		var err error
		ret, res, err = f()
		return err
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = timeout

	backoffErr := backoff.RetryNotify(op, b, func(err error, duration time.Duration) {
		// Round to a whole number of milliseconds
		duration /= retryBackoffRoundRatio // Convert nanoseconds to milliseconds
		duration *= retryBackoffRoundRatio // Convert back so it appears correct

		log.Errorf("Error performing operation; retrying in %v: %v", duration, err)
	})

	return ret, res, backoffErr
}

// NewClient creates a Client and returns it; which
// implementation it uses depends on the configuration of this
// run. For example, a dry-run clients may be created which does
// not make any requests that would change anything on the server,
// but instead simply prints out the actions that it's asked to take.
func NewClient(config cfg.Config) (Client, error) {
	var ret Client

	log := config.GetLogger()

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: config.GetConfigString("github-token")},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	ret = realGHClient{
		client: *client,
	}

	// Make a request so we can check that we can connect fine.
	_, _, err := ret.getRateLimits(context.Background())
	if err != nil {
		return realGHClient{}, err
	}
	log.Debug("Successfully connected to GitHub.")

	return ret, nil
}

func NewTestClient() TestGHClient {
	return TestGHClient {}
}

