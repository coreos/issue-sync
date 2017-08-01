package lib

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/coreos/issue-sync/cfg"
	"github.com/coreos/issue-sync/cli"
	"github.com/google/go-github/github"
)

// dateFormat is the format used for the Last IS Update field
const dateFormat = "2006-01-02T15:04:05-0700"

// commentDateFormat is the format used in the headers of JIRA comments
const commentDateFormat = "15:04 PM, January 2 2006"

// CompareIssues gets the list of GitHub issues updated since the `since` date,
// gets the list of JIRA issues which have GitHub ID custom fields in that list,
// then matches each one. If a JIRA issue already exists for a given GitHub issue,
// it calls UpdateIssue; if no JIRA issue already exists, it calls CreateIssue.
func CompareIssues(config cfg.Config, ghClient github.Client, jiraClient jira.Client) error {
	log := config.GetLogger()

	log.Debug("Collecting issues")
	ctx := context.Background()

	user, repo := config.GetRepo()

	i, _, err := cli.MakeGHRequest(config, func() (interface{}, *github.Response, error) {
		return ghClient.Issues.ListByRepo(ctx, user, repo, &github.IssueListByRepoOptions{
			Since: config.GetSinceParam(),
			State: "all",
			ListOptions: github.ListOptions{
				PerPage: 100,
			},
		})
	})
	if err != nil {
		return err
	}
	ghIssues, ok := i.([]*github.Issue)
	if !ok {
		log.Errorf("Get GitHub issues did not return issues! Got: %v", i)
		return errors.New(fmt.Sprintf("Get GitHub issues failed: expected []*github.Issue; got %T", i))
	}
	if len(ghIssues) == 0 {
		log.Info("There are no GitHub issues; exiting")
		return nil
	}
	log.Debug("Collected all GitHub issues")

	ids := make([]string, len(ghIssues))
	for i, v := range ghIssues {
		ids[i] = fmt.Sprint(*v.ID)
	}

	jql := fmt.Sprintf("project='%s' AND cf[%s] in (%s)",
		config.GetProjectKey(), config.GetFieldID(cfg.GitHubID), strings.Join(ids, ","))

	ji, res, err := cli.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
		return jiraClient.Issue.Search(jql, nil)
	})
	if err != nil {
		log.Errorf("Error retrieving JIRA issues: %s", err)
		return cli.GetErrorBody(config, res)
	}
	jiraIssues, ok := ji.([]jira.Issue)
	if !ok {
		log.Errorf("Get JIRA issues did not return issues! Got: %v", ji)
		return errors.New(fmt.Sprintf("Get JIRA issues failed: expected []jira.Issue; got %T", ji))
	}

	log.Debug("Collected all JIRA issues")

	for _, ghIssue := range ghIssues {
		found := false
		for _, jIssue := range jiraIssues {
			id, _ := jIssue.Fields.Unknowns.Int(config.GetFieldKey(cfg.GitHubID))
			if int64(*ghIssue.ID) == id {
				found = true
				if err := UpdateIssue(config, *ghIssue, jIssue, ghClient, jiraClient); err != nil {
					log.Errorf("Error updating issue %s. Error: %v", jIssue.Key, err)
				}
				break
			}
		}
		if !found {
			if err := CreateIssue(config, *ghIssue, ghClient, jiraClient); err != nil {
				log.Errorf("Error creating issue for #%d. Error: %v", *ghIssue.Number, err)
			}
		}
	}

	return nil
}

// newlineReplaceRegex is a regex to match both "\r\n" and just "\n" newline styles,
// in order to allow us to escape both sequences cleanly in the output of a dry run.
var newlineReplaceRegex = regexp.MustCompile("\r?\n")

// DidIssueChange tests each of the relevant fields on the provided JIRA and GitHub issue
// and returns whether or not they differ.
func DidIssueChange(config cfg.Config, ghIssue github.Issue, jIssue jira.Issue) bool {
	log := config.GetLogger()

	log.Debugf("Comparing GitHub issue #%d and JIRA issue %s", ghIssue.GetNumber(), jIssue.Key)

	anyDifferent := false

	anyDifferent = anyDifferent || (ghIssue.GetTitle() != jIssue.Fields.Summary)
	anyDifferent = anyDifferent || (ghIssue.GetBody() != jIssue.Fields.Description)

	key := config.GetFieldKey(cfg.GitHubStatus)
	field, err := jIssue.Fields.Unknowns.String(key)
	if err != nil || *ghIssue.State != field {
		anyDifferent = true
	}

	key = config.GetFieldKey(cfg.GitHubReporter)
	field, err = jIssue.Fields.Unknowns.String(key)
	if err != nil || *ghIssue.User.Login != field {
		anyDifferent = true
	}

	labels := make([]string, len(ghIssue.Labels))
	for i, l := range ghIssue.Labels {
		labels[i] = *l.Name
	}

	key = config.GetFieldKey(cfg.GitHubLabels)
	field, err = jIssue.Fields.Unknowns.String(key)
	if err != nil && strings.Join(labels, ",") != field {
		anyDifferent = true
	}

	log.Debugf("Issues have any differences: %b", anyDifferent)

	return anyDifferent
}

// UpdateIssue compares each field of a GitHub issue to a JIRA issue; if any of them
// differ, the differing fields of the JIRA issue are updated to match the GitHub
// issue.
func UpdateIssue(config cfg.Config, ghIssue github.Issue, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	log.Debugf("Updating JIRA %s with GitHub #%d", jIssue.Key, *ghIssue.Number)

	if DidIssueChange(config, ghIssue, jIssue) {
		fields := jira.IssueFields{}
		fields.Unknowns = map[string]interface{}{}

		fields.Summary = ghIssue.GetTitle()
		fields.Description = ghIssue.GetBody()
		fields.Unknowns[config.GetFieldKey(cfg.GitHubStatus)] = ghIssue.GetState()
		fields.Unknowns[config.GetFieldKey(cfg.GitHubReporter)] = ghIssue.User.GetLogin()

		labels := make([]string, len(ghIssue.Labels))
		for i, l := range ghIssue.Labels {
			labels[i] = l.GetName()
		}
		fields.Unknowns[config.GetFieldKey(cfg.GitHubLabels)] = strings.Join(labels, ",")

		fields.Unknowns[config.GetFieldKey(cfg.LastISUpdate)] = time.Now().Format(dateFormat)

		fields.Type = jIssue.Fields.Type

		issue := &jira.Issue{
			Fields: &fields,
			Key:    jIssue.Key,
			ID:     jIssue.ID,
		}

		if !config.IsDryRun() {
			_, res, err := cli.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
				return jClient.Issue.Update(issue)
			})

			if err != nil {
				log.Errorf("Error updating JIRA issue %s: %v", jIssue.Key, err)
				return cli.GetErrorBody(config, res)
			}
		} else {
			log.Info("")
			log.Infof("Update JIRA issue %s with GitHub issue #%d:", jIssue.Key, ghIssue.GetNumber())
			if fields.Summary != jIssue.Fields.Summary {
				log.Infof("  Summary: %s", fields.Summary)
			}
			if fields.Description != "" {
				fields.Description = newlineReplaceRegex.ReplaceAllString(fields.Description, "\\n")
				if len(fields.Description) > 20 {
					log.Infof("  Description: %s...", fields.Description[0:20])
				} else {
					log.Infof("  Description: %s", fields.Description)
				}
			}
			key := config.GetFieldKey(cfg.GitHubLabels)
			if labels, err := fields.Unknowns.String(key); err == nil {
				log.Infof("  Labels: %s", labels)
			}
			key = config.GetFieldKey(cfg.GitHubStatus)
			if state, err := fields.Unknowns.String(key); err == nil {
				log.Infof("  State: %s", state)
			}
			log.Info("")
		}

		log.Debugf("Successfully updated JIRA issue %s!", jIssue.Key)
	} else {
		log.Debugf("JIRA issue %s is already up to date!", jIssue.Key)
	}

	i, _, err := cli.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
		return jClient.Issue.Get(jIssue.ID, nil)
	})
	if err != nil {
		log.Errorf("Error retrieving JIRA issue %s to get comments.", jIssue.Key)
	}
	issue, ok := i.(*jira.Issue)
	if !ok {
		log.Errorf("Get JIRA issue did not return issue! Got: %v", i)
		return errors.New(fmt.Sprintf("Get JIRA issue failed: expected *jira.Issue; got %T", i))
	}

	var comments []jira.Comment
	if issue.Fields.Comments == nil {
		log.Debugf("JIRA issue %s has no comments.", jIssue.Key)
	} else {
		commentPtrs := issue.Fields.Comments.Comments
		comments = make([]jira.Comment, len(commentPtrs))
		for i, v := range commentPtrs {
			comments[i] = *v
		}
		log.Debugf("JIRA issue %s has %d comments", jIssue.Key, len(comments))
	}

	if err = CompareComments(config, ghIssue, jIssue, comments, ghClient, jClient); err != nil {
		return err
	}

	return nil
}

// CreateIssue generates a JIRA issue from the various fields on the given GitHub issue, then
// sends it to the JIRA API.
func CreateIssue(config cfg.Config, issue github.Issue, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	log.Debugf("Creating JIRA issue based on GitHub issue #%d", *issue.Number)

	fields := jira.IssueFields{
		Type: jira.IssueType{
			Name: "Task", // TODO: Determine issue type
		},
		Project:     config.GetProject(),
		Summary:     *issue.Title,
		Description: *issue.Body,
		Unknowns:    map[string]interface{}{},
	}

	key := config.GetFieldKey(cfg.GitHubID)
	fields.Unknowns[key] = *issue.ID
	key = config.GetFieldKey(cfg.GitHubNumber)
	fields.Unknowns[key] = *issue.Number
	key = config.GetFieldKey(cfg.GitHubStatus)
	fields.Unknowns[key] = *issue.State
	key = config.GetFieldKey(cfg.GitHubReporter)
	fields.Unknowns[key] = issue.User.GetLogin()
	key = config.GetFieldKey(cfg.GitHubLabels)
	strs := make([]string, len(issue.Labels))
	for i, v := range issue.Labels {
		strs[i] = *v.Name
	}
	fields.Unknowns[key] = strings.Join(strs, ",")
	key = config.GetFieldKey(cfg.LastISUpdate)
	fields.Unknowns[key] = time.Now().Format(dateFormat)

	jIssue := &jira.Issue{
		Fields: &fields,
	}

	if !config.IsDryRun() {
		i, res, err := cli.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
			return jClient.Issue.Create(jIssue)
		})
		if err != nil {
			log.Errorf("Error creating JIRA issue: %v", err)
			return cli.GetErrorBody(config, res)
		}
		var ok bool
		jIssue, ok = i.(*jira.Issue)
		if !ok {
			log.Errorf("Create JIRA issue did not return issue! Got: %v", i)
			return errors.New(fmt.Sprintf("Create JIRA issue failed: expected *jira.Issue; got %T", i))
		}
	} else {
		log.Info("")
		log.Infof("Create JIRA issue for GitHub issue #%d:", issue.GetNumber())
		log.Infof("  Summary: %s", fields.Summary)
		if fields.Description == "" {
			log.Infof("  Description: empty")
		} else {
			fields.Description = newlineReplaceRegex.ReplaceAllString(fields.Description, "\\n")
			if len(fields.Description) <= 20 {
				log.Infof("  Description: %s", fields.Description)
			} else {
				log.Infof("  Description: %s...", fields.Description[0:20])
			}
		}
		key := config.GetFieldKey(cfg.GitHubLabels)
		log.Infof("  Labels: %s", fields.Unknowns[key])
		key = config.GetFieldKey(cfg.GitHubStatus)
		log.Infof("  State: %s", fields.Unknowns[key])
		key = config.GetFieldKey(cfg.GitHubReporter)
		log.Infof("  Reporter: %s", fields.Unknowns[key])
		log.Info("")
	}

	log.Debugf("Created JIRA issue %s!", jIssue.Key)

	if err := CompareComments(config, issue, *jIssue, nil, ghClient, jClient); err != nil {
		return err
	}

	return nil
}
