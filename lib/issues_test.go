package lib

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"fmt"

	"github.com/andygrunwald/go-jira"
	"github.com/coreos/issue-sync/lib/clients"
	"github.com/google/go-github/github"
)

/*
 * There are three testing GitHub issues. The first has two comments, the second and third have 1.
 * The first and second are open, and the third closed. There are three associated users: tester,
 * testguy, and test3. The first issue has an identical JIRA issue, although only one comment has
 * been copied. The second issue has a matching, but outdated, JIRA issue, so it will be updated.
 * The third issue has no JIRA issue, so a new one will be created.
 */

var ghIssues []github.Issue

var ghComments map[int][]*github.IssueComment

var jiraIssues = []jira.Issue{
	{
		ID: "10001",
		Fields: &jira.IssueFields{
			Summary:     "Test Issue 1",
			Description: "First issue used for testing",
			Comments: &jira.Comments{
				Comments: []*jira.Comment{
					{
						ID: "10002",
						Body: "Comment ([ID 10|http://example.com/comment/10]) from GitHub User " +
							"[tester|http://example.com/tester] (Test User) at 12:00 PM, August 22 2017:\n\n" +
							"First comment on the first issue!",
					},
				},
			},
			Unknowns: map[string]interface{}{
				fmt.Sprintf("customfield_%s", ghIDFieldID):         int64(1),
				fmt.Sprintf("customfield_%s", ghNumberFieldID):     int64(1),
				fmt.Sprintf("customfield_%s", ghLabelsFieldID):     "label1",
				fmt.Sprintf("customfield_%s", ghStatusFieldID):     "open",
				fmt.Sprintf("customfield_%s", ghReporterFieldID):   "tester",
				fmt.Sprintf("customfield_%s", isLastUpdateFieldID): "2017-08-21T00:00:00Z",
			},
		},
	},
	{
		ID: "10002",
		Fields: &jira.IssueFields{
			Summary:     "Test Issue 2",
			Description: "Second issue used for testing",
			Comments: &jira.Comments{
				Comments: []*jira.Comment{
					{
						ID: "10002",
						Body: "Comment ([ID 11|http://example.com/comment/11]) from GitHub User " +
							"[testguy|http://example.com/testguy] (Another Test User) at 18:00 PM, August 22 2017:\n\n" +
							"First comment on the second issue!",
					},
				},
			},
			Unknowns: map[string]interface{}{
				fmt.Sprintf("customfield_%s", ghIDFieldID):         int64(2),
				fmt.Sprintf("customfield_%s", ghNumberFieldID):     int64(2),
				fmt.Sprintf("customfield_%s", ghLabelsFieldID):     "label2,label3",
				fmt.Sprintf("customfield_%s", ghStatusFieldID):     "open",
				fmt.Sprintf("customfield_%s", ghReporterFieldID):   "testguy",
				fmt.Sprintf("customfield_%s", isLastUpdateFieldID): "2017-08-21T00:00:00Z",
			},
		},
	},
}

type testGHClient struct {
}

func (testGHClient) ListIssues() ([]github.Issue, error) {
	return ghIssues, nil
}

func (testGHClient) ListComments(issue github.Issue) ([]*github.IssueComment, error) {
	return ghComments[issue.GetID()], nil
}

func (testGHClient) GetUser(login string) (github.User, error) {
	for _, v := range ghIssues {
		if v.User.GetLogin() == login {
			return *v.User, nil
		}
	}
	return github.User{}, nil
}

func (testGHClient) GetRateLimits() (github.RateLimits, error) {
	return github.RateLimits{
		Core: &github.Rate{
			Limit:     1<<32 - 1,
			Remaining: 1<<32 - 1,
			Reset: github.Timestamp{
				Time: time.Date(2100, 1, 1, 0, 0, 0, 0, nil),
			},
		},
	}, nil
}

type comment struct {
	Issue     jira.Issue
	ID        string
	GHComment github.IssueComment
}

type testJIRAClient struct {
	CreatedIssues   []jira.Issue
	UpdatedIssues   []jira.Issue
	CreatedComments []comment
	UpdatedComments []comment
}

func (testJIRAClient) ListIssues(ids []int) ([]jira.Issue, error) {
	return jiraIssues, nil
}

func (testJIRAClient) GetIssue(key string) (jira.Issue, error) {
	for _, v := range jiraIssues {
		if v.Key == key {
			return v, nil
		}
	}
	return jira.Issue{}, nil
}

func (j testJIRAClient) CreateIssue(issue jira.Issue) (jira.Issue, error) {
	j.CreatedIssues = append(j.CreatedIssues, issue)
	return issue, nil
}

func (j testJIRAClient) UpdateIssue(issue jira.Issue) (jira.Issue, error) {
	j.UpdatedIssues = append(j.UpdatedIssues, issue)
	return issue, nil
}

func (j testJIRAClient) CreateComment(issue jira.Issue, cmnt github.IssueComment, github clients.GitHubClient) (jira.Comment, error) {
	j.CreatedComments = append(j.CreatedComments, comment{
		Issue:     issue,
		ID:        "",
		GHComment: cmnt,
	})

	return jira.Comment{}, nil
}

func (j testJIRAClient) UpdateComment(issue jira.Issue, id string, cmnt github.IssueComment, github clients.GitHubClient) (jira.Comment, error) {
	j.UpdatedComments = append(j.UpdatedComments, comment{
		Issue:     issue,
		ID:        id,
		GHComment: cmnt,
	})

	return jira.Comment{}, nil
}

func setup() error {
	file, err := os.Open("sample_gh_issues.json")
	if err != nil {
		return err
	}
	jsonData, err := ioutil.ReadAll(file)
	if err != nil {
		return err
	}
	err = json.Unmarshal(jsonData, &ghIssues)
	if err != nil {
		return err
	}

	file, err = os.Open("sample_comments.json")
	if err != nil {
		return err
	}
	jsonData, err = ioutil.ReadAll(file)
	if err != nil {
		return err
	}
	err = json.Unmarshal(jsonData, &ghComments)
	return err
}

func TestCompareIssues(t *testing.T) {
	if err := setup(); err != nil {
		t.Fatalf("Error running setup: %v", err)
	}

	gh := testGHClient{}
	jc := testJIRAClient{}

	config := NewTestConfig()

	updates, creates, err := CompareIssues(config, gh, jc)
	if err != nil {
		t.Fatalf("Error occured while comparing issues: %v", err)
	}

	if len(updates) != 1 {
		t.Fatalf("Number of updates wrong: expected 1; got %d", len(updates))
	}
	if updates[0].gh.ID != ghIssues[1].ID {
		t.Errorf("Update pair GitHub issue ID wrong: expected %d; got %d", ghIssues[1].ID, updates[0].gh.ID)
	}
	if updates[0].j.ID != jiraIssues[1].ID {
		t.Errorf("Update pair JIRA issue ID wrong: expected %d; got %d", jiraIssues[1].ID, updates[0].j.ID)
	}

	if len(creates) != 1 {
		t.Fatalf("Number of creates wrong: expected 1; got %d", len(creates))
	}
	if creates[0].ID != ghIssues[2].ID {
		t.Errorf("Create issue ID wrong: expected %d; got %d", ghIssues[2].ID, creates[0].ID)
	}
}
