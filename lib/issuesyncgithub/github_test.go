package issuesyncgithub

import (
	"context"
	"github.com/coreos/issue-sync/cfg"
	"github.com/google/go-github/github"
	"testing"
	"time"
)


func TestListIssues(t *testing.T) {

	client := NewTestClient()

	client.handleGetRepository = func(ctx context.Context, owner string, repo string) (repository *github.Repository, response *github.Response, e error) {
		hasProject := true
		return &github.Repository{ HasProjects: &hasProject}, nil, nil
	}

	log := *cfg.NewLogger("test", "debug")

	issues, err := ListIssues(client, log, 10, "", "", time.Now())

	print(issues)
	print(err)
}
