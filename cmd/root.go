package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/andygrunwald/go-jira"
	"github.com/coreos/issue-sync/lib"
	"github.com/google/go-github/github"
	"github.com/spf13/cobra"
)

// dateFormat is the format used for the `Last Issue-Sync Update` field.
const dateFormat = "2006-01-02T15:04:05-0700"

// commentDateFormat is the format used in the headers of JIRA comments.
const commentDateFormat = "15:04 PM, January 2 2006"

// Execute provides a single function to run the root command and handle errors.
func Execute() {
	// Create a temporary logger that we can use if an error occurs before the real one is instantiated.
	log := logrus.New()
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

// RootCmd represents the command itself and configures it.
var RootCmd = &cobra.Command{
	Use:   "issue-sync [options]",
	Short: "A tool to synchronize GitHub and JIRA issues",
	Long:  "Full docs coming later; see https://github.com/coreos/issue-sync",
	RunE: func(cmd *cobra.Command, args []string) error {
		config, err := lib.NewConfig(cmd)
		if err != nil {
			return err
		}

		ghClient, err := lib.GetGitHubClient(config)
		if err != nil {
			return err
		}
		jiraClient, err := lib.GetJIRAClient(config)
		if err != nil {
			return err
		}

		if err := config.LoadJIRAConfig(*jiraClient); err != nil {
			return err
		}

		if err := compareIssues(config, *ghClient, *jiraClient); err != nil {
			return err
		}

		if !config.IsDryRun() {
			return config.SaveConfig()
		}

		return nil
	},
}

// compareIssues gets the list of GitHub issues updated since the `since` date,
// gets the list of JIRA issues which have GitHub ID custom fields in that list,
// then matches each one. If a JIRA issue already exists for a given GitHub issue,
// it updates the issue; if no JIRA issue already exists, it creates one.
func compareIssues(config lib.Config, ghClient github.Client, jiraClient jira.Client) error {
	log := config.GetLogger()

	log.Debug("Collecting issues")
	ctx := context.Background()

	user, repo := config.GetRepo()

	i, _, err := lib.MakeGHRequest(config, func() (interface{}, *github.Response, error) {
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
		return fmt.Errorf("get GitHub issues failed: expected []*github.Issue; got %T", i)
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
		config.GetProjectKey(), config.GetFieldID(lib.GitHubID), strings.Join(ids, ","))

	ji, res, err := lib.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
		return jiraClient.Issue.Search(jql, nil)
	})
	if err != nil {
		log.Errorf("Error retrieving JIRA issues: %v", err)
		return lib.GetErrorBody(config, res)
	}
	jiraIssues, ok := ji.([]jira.Issue)
	if !ok {
		log.Errorf("Get JIRA issues did not return issues! Got: %v", ji)
		return fmt.Errorf("get JIRA issues failed: expected []jira.Issue; got %T", ji)
	}

	log.Debug("Collected all JIRA issues")

	for _, ghIssue := range ghIssues {
		found := false
		for _, jIssue := range jiraIssues {
			id, err := jIssue.Fields.Unknowns.Int(config.GetFieldKey(lib.GitHubID))
			if err == nil && int64(*ghIssue.ID) == id {
				found = true
				if err := updateIssue(config, *ghIssue, jIssue, ghClient, jiraClient); err != nil {
					log.Errorf("Error updating issue %s. Error: %v", jIssue.Key, err)
				}
				break
			}
		}
		if !found {
			if err := createIssue(config, *ghIssue, ghClient, jiraClient); err != nil {
				log.Errorf("Error creating issue for #%d. Error: %v", *ghIssue.Number, err)
			}
		}
	}

	return nil
}

// newlineReplaceRegex is a regex to match both "\r\n" and just "\n" newline styles,
// in order to allow us to escape both sequences cleanly in the output of a dry run.
var newlineReplaceRegex = regexp.MustCompile("\r?\n")

// updateIssue compares each field of a GitHub issue to a JIRA issue; if any of them
// differ, the differing fields of the JIRA issue are updated to match the GitHub
// issue.
func updateIssue(config lib.Config, ghIssue github.Issue, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	log.Debugf("Updating JIRA %s with GitHub #%d", jIssue.Key, *ghIssue.Number)

	anyDifferent := false

	fields := jira.IssueFields{}
	fields.Unknowns = map[string]interface{}{}

	if *ghIssue.Title != jIssue.Fields.Summary {
		anyDifferent = true
		fields.Summary = *ghIssue.Title
	}

	if *ghIssue.Body != jIssue.Fields.Description {
		anyDifferent = true
		fields.Description = *ghIssue.Body
	}

	key := config.GetFieldKey(lib.GitHubStatus)
	field, err := jIssue.Fields.Unknowns.String(key)
	if err != nil || *ghIssue.State != field {
		anyDifferent = true
		fields.Unknowns[key] = *ghIssue.State
	}

	key = config.GetFieldKey(lib.GitHubReporter)
	field, err = jIssue.Fields.Unknowns.String(key)
	if err != nil || *ghIssue.User.Login != field {
		anyDifferent = true
		fields.Unknowns[key] = *ghIssue.User.Login
	}

	labels := make([]string, len(ghIssue.Labels))
	for i, l := range ghIssue.Labels {
		labels[i] = *l.Name
	}

	key = config.GetFieldKey(lib.GitHubLabels)
	field, err = jIssue.Fields.Unknowns.String(key)
	if err != nil && strings.Join(labels, ",") != field {
		anyDifferent = true
		fields.Unknowns[key] = strings.Join(labels, ",")
	}

	if anyDifferent {
		key = config.GetFieldKey(lib.LastISUpdate)
		fields.Unknowns[key] = time.Now().Format(dateFormat)

		fields.Type = jIssue.Fields.Type
		if fields.Summary == "" {
			fields.Summary = jIssue.Fields.Summary
		}

		issue := &jira.Issue{
			Fields: &fields,
			Key:    jIssue.Key,
			ID:     jIssue.ID,
		}

		if !config.IsDryRun() {
			_, res, err := lib.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
				return jClient.Issue.Update(issue)
			})

			if err != nil {
				log.Errorf("Error updating JIRA issue %s: %v", jIssue.Key, err)
				return lib.GetErrorBody(config, res)
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
			key := config.GetFieldKey(lib.GitHubLabels)
			if labels, err := fields.Unknowns.String(key); err == nil {
				log.Infof("  Labels: %s", labels)
			}
			key = config.GetFieldKey(lib.GitHubStatus)
			if state, err := fields.Unknowns.String(key); err == nil {
				log.Infof("  State: %s", state)
			}
			log.Info("")
		}

		log.Debugf("Successfully updated JIRA issue %s!", jIssue.Key)
	} else {
		log.Debugf("JIRA issue %s is already up to date!", jIssue.Key)
	}

	i, _, err := lib.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
		return jClient.Issue.Get(jIssue.ID, nil)
	})
	if err != nil {
		log.Errorf("Error retrieving JIRA issue %s to get comments.", jIssue.Key)
	}
	issue, ok := i.(*jira.Issue)
	if !ok {
		log.Errorf("Get JIRA issue did not return issue! Got: %v", i)
		return fmt.Errorf("get JIRA issue failed: expected *jira.Issue; got %T", i)
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

	if err = createComments(config, ghIssue, jIssue, comments, ghClient, jClient); err != nil {
		return err
	}

	return nil
}

// createIssue generates a JIRA issue from the various fields on the given GitHub issue then
// sends it to the JIRA API.
func createIssue(config lib.Config, issue github.Issue, ghClient github.Client, jClient jira.Client) error {
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

	key := config.GetFieldKey(lib.GitHubID)
	fields.Unknowns[key] = *issue.ID
	key = config.GetFieldKey(lib.GitHubNumber)
	fields.Unknowns[key] = *issue.Number
	key = config.GetFieldKey(lib.GitHubStatus)
	fields.Unknowns[key] = *issue.State
	key = config.GetFieldKey(lib.GitHubReporter)
	fields.Unknowns[key] = issue.User.GetLogin()
	key = config.GetFieldKey(lib.GitHubLabels)
	strs := make([]string, len(issue.Labels))
	for i, v := range issue.Labels {
		strs[i] = *v.Name
	}
	fields.Unknowns[key] = strings.Join(strs, ",")
	key = config.GetFieldKey(lib.LastISUpdate)
	fields.Unknowns[key] = time.Now().Format(dateFormat)

	jIssue := &jira.Issue{
		Fields: &fields,
	}

	if !config.IsDryRun() {
		i, res, err := lib.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
			return jClient.Issue.Create(jIssue)
		})
		if err != nil {
			log.Errorf("Error creating JIRA issue: %v", err)
			return lib.GetErrorBody(config, res)
		}
		var ok bool
		jIssue, ok = i.(*jira.Issue)
		if !ok {
			log.Errorf("Create JIRA issue did not return issue! Got: %v", i)
			return fmt.Errorf("create JIRA issue failed: expected *jira.Issue; got %T", i)
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
		key := config.GetFieldKey(lib.GitHubLabels)
		log.Infof("  Labels: %s", fields.Unknowns[key])
		key = config.GetFieldKey(lib.GitHubStatus)
		log.Infof("  State: %s", fields.Unknowns[key])
		key = config.GetFieldKey(lib.GitHubReporter)
		log.Infof("  Reporter: %s", fields.Unknowns[key])
		log.Info("")
	}

	log.Debugf("Created JIRA issue %s!", jIssue.Key)

	if err := createComments(config, issue, *jIssue, nil, ghClient, jClient); err != nil {
		return err
	}

	return nil
}

// jCommentRegex matches a generated JIRA comment. It has matching groups to retrieve the
// GitHub Comment ID (\1), the GitHub username (\2), the GitHub real name (\3, if it exists),
// the time the comment was posted (\3 or \4), and the body of the comment (\4 or \5).
var jCommentRegex = regexp.MustCompile("^Comment \\(ID (\\d+)\\) from GitHub user (\\w+) \\((.+)\\)? at (.+):\\n\\n(.+)$")

// jCommentIDRegex just matches the beginning of a generated JIRA comment. It's a smaller,
// simpler, and more efficient regex, to quickly filter only generated comments and retrieve
// just their GitHub ID for matching.
var jCommentIDRegex = regexp.MustCompile("^Comment \\(ID (\\d+)\\)")

// createCommments takes a GitHub issue and retrieves all of its comments. It then
// matches each one to a comment in `existing`. If it finds a match, it calls
// updateComment; if it doesn't, it calls createComment.
func createComments(config lib.Config, ghIssue github.Issue, jIssue jira.Issue, existing []jira.Comment, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	if *ghIssue.Comments == 0 {
		log.Debugf("Issue #%d has no comments, skipping.", *ghIssue.Number)
		return nil
	}

	ctx := context.Background()
	user, repo := config.GetRepo()
	c, _, err := lib.MakeGHRequest(config, func() (interface{}, *github.Response, error) {
		return ghClient.Issues.ListComments(ctx, user, repo, *ghIssue.Number, &github.IssueListCommentsOptions{
			Sort:      "created",
			Direction: "asc",
		})
	})
	if err != nil {
		log.Errorf("Error retrieving GitHub comments for issue #%d. Error: %v.", *ghIssue.Number, err)
		return err
	}
	comments, ok := c.([]*github.IssueComment)
	if !ok {
		log.Errorf("Get GitHub comments did not return comments! Got: %v", c)
		return fmt.Errorf("Get GitHub comments failed: expected []*github.IssueComment; got %T", c)
	}

	for _, ghComment := range comments {
		found := false
		for _, jComment := range existing {
			if !jCommentIDRegex.MatchString(jComment.Body) {
				continue
			}
			// matches[0] is the whole string, matches[1] is the ID
			matches := jCommentIDRegex.FindStringSubmatch(jComment.Body)
			id, _ := strconv.Atoi(matches[1])
			if *ghComment.ID != id {
				continue
			}
			found = true

			updateComment(config, *ghComment, jComment, jIssue, ghClient, jClient)
			break
		}
		if found {
			continue
		}

		if err := createComment(config, *ghComment, jIssue, ghClient, jClient); err != nil {
			return err
		}
	}

	log.Debugf("Copied comments from GH issue #%d to JIRA issue %s.", *ghIssue.Number, jIssue.Key)
	return nil
}

// updateComment compares the body of a GitHub comment with the body (minus header)
// of the JIRA comment, and updates the JIRA comment if necessary.
func updateComment(config lib.Config, ghComment github.IssueComment, jComment jira.Comment, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	// fields[0] is the whole body, 1 is the ID, 2 is the username, 3 is the real name (or "" if none)
	// 4 is the date, and 5 is the real body
	fields := jCommentRegex.FindStringSubmatch(jComment.Body)

	if fields[5] == *ghComment.Body {
		return nil
	}

	u, _, err := lib.MakeGHRequest(config, func() (interface{}, *github.Response, error) {
		return ghClient.Users.Get(context.Background(), *ghComment.User.Login)
	})
	if err != nil {
		log.Errorf("Error retrieving GitHub user %s: %v", *ghComment.User.Login, err)
	}
	user, ok := u.(*github.User)
	if !ok {
		log.Errorf("Get GitHub user did not return user! Got: %v", u)
		return fmt.Errorf("get GitHub user failed: expected *github.User; got %T", u)
	}

	body := fmt.Sprintf("Comment (ID %d) from GitHub user %s", *ghComment.ID, user.GetLogin())
	if user.GetName() != "" {
		body = fmt.Sprintf("%s (%s)", body, user.GetName())
	}
	body = fmt.Sprintf(
		"%s at %s:\n\n%s",
		body,
		ghComment.CreatedAt.Format(commentDateFormat),
		*ghComment.Body,
	)

	// As it is, the JIRA API we're using doesn't have any way to update comments natively.
	// So, we have to build the request ourselves.

	request := struct {
		Body string `json:"body"`
	}{
		Body: body,
	}

	if !config.IsDryRun() {
		req, err := jClient.NewRequest("PUT", fmt.Sprintf("rest/api/2/issue/%s/comment/%s", jIssue.Key, jComment.ID), request)
		if err != nil {
			log.Errorf("Error creating comment update request: %v", err)
			return err
		}

		_, res, err := lib.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
			res, err := jClient.Do(req, nil)
			return nil, res, err
		})
		if err != nil {
			log.Errorf("Error updating comment: %v", err)
			return lib.GetErrorBody(config, res)
		}
	} else {
		log.Info("")
		log.Infof("Update JIRA comment %s on issue %s:", jComment.ID, jIssue.Key)
		if request.Body == "" {
			log.Info("  Body: empty")
		} else {
			request.Body = newlineReplaceRegex.ReplaceAllString(request.Body, "\\n")
			if len(request.Body) <= 150 {
				log.Infof("  Body: %s", request.Body)
			} else {
				log.Infof("  Body: %s...", request.Body[0:150])
			}
		}
		log.Info("")
	}

	return nil
}

// createComment uses the ID, poster username, poster name, created at time, and body
// of a GitHub comment to generate the body of a JIRA comment, then creates it in the
// API.
func createComment(config lib.Config, ghComment github.IssueComment, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	u, _, err := lib.MakeGHRequest(config, func() (interface{}, *github.Response, error) {
		return ghClient.Users.Get(context.Background(), *ghComment.User.Login)
	})
	if err != nil {
		log.Errorf("Error retrieving GitHub user %s. Error: %v", *ghComment.User.Login, err)
		return err
	}
	user, ok := u.(*github.User)
	if !ok {
		log.Errorf("Get GitHub user did not return user! Got: %v", u)
		return fmt.Errorf("Get GitHub user failed: expected *github.User; got %T", u)
	}

	body := fmt.Sprintf("Comment (ID %d) from GitHub user %s", *ghComment.ID, user.GetLogin())
	if user.GetName() != "" {
		body = fmt.Sprintf("%s (%s)", body, user.GetName())
	}
	body = fmt.Sprintf(
		"%s at %s:\n\n%s",
		body,
		ghComment.CreatedAt.Format(commentDateFormat),
		*ghComment.Body,
	)
	jComment := &jira.Comment{
		Body: body,
	}

	if !config.IsDryRun() {
		_, res, err := lib.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
			return jClient.Issue.AddComment(jIssue.ID, jComment)
		})
		if err != nil {
			log.Errorf("Error creating JIRA comment on issue %s. Error: %v", jIssue.Key, err)
			return lib.GetErrorBody(config, res)
		}
	} else {
		log.Info("")
		log.Infof("Create comment on JIRA issue %s:", jIssue.Key)
		log.Infof("  GitHub Comment ID: %d", ghComment.GetID())
		log.Infof("  GitHub user login: %s", ghComment.User.GetLogin())
		log.Infof("  Github user name: %s", ghComment.User.GetName())
		log.Infof("  Created date: %s", ghComment.GetCreatedAt().Format(commentDateFormat))
		if ghComment.GetBody() == "" {
			log.Info("  Body: empty")
		} else {
			body := newlineReplaceRegex.ReplaceAllString(ghComment.GetBody(), "\\n")
			if len(body) <= 20 {
				log.Infof("  Body: %s", body)
			} else {
				log.Infof("  Body: %s...", body[0:20])
			}
		}
		log.Info("")
	}

	return nil
}

func init() {
	RootCmd.PersistentFlags().String("log-level", logrus.InfoLevel.String(), "Set the global log level")
	RootCmd.PersistentFlags().String("config", "", "Config file (default is $HOME/.issue-sync.json)")
	RootCmd.PersistentFlags().StringP("github-token", "t", "", "Set the API Token used to access the GitHub repo")
	RootCmd.PersistentFlags().StringP("jira-user", "u", "", "Set the JIRA username to authenticate with")
	RootCmd.PersistentFlags().StringP("jira-pass", "p", "", "Set the JIRA password to authenticate with")
	RootCmd.PersistentFlags().StringP("repo-name", "r", "", "Set the repository path (should be form owner/repo)")
	RootCmd.PersistentFlags().StringP("jira-uri", "U", "", "Set the base uri of the JIRA instance")
	RootCmd.PersistentFlags().StringP("jira-project", "P", "", "Set the key of the JIRA project")
	RootCmd.PersistentFlags().StringP("since", "s", "1970-01-01T00:00:00+0000", "Set the day that the update should run forward from")
	RootCmd.PersistentFlags().BoolP("dry-run", "d", false, "Print out actions to be taken, but do not execute them")
	RootCmd.PersistentFlags().DurationP("timeout", "T", time.Minute, "Set the maximum timeout on all API calls")
}
