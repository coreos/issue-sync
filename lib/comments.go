package lib

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/andygrunwald/go-jira"
	"github.com/coreos/issue-sync/cfg"
	"github.com/coreos/issue-sync/cli"
	"github.com/google/go-github/github"
)

// jCommentRegex matches a generated JIRA comment. It has matching groups to retrieve the
// GitHub Comment ID (\1), the GitHub username (\2), the GitHub real name (\3, if it exists),
// the time the comment was posted (\3 or \4), and the body of the comment (\4 or \5).
var jCommentRegex = regexp.MustCompile("^Comment \\(ID (\\d+)\\) from GitHub user (\\w+) \\((.+)\\)? at (.+):\\n\\n(.+)$")

// jCommentIDRegex just matches the beginning of a generated JIRA comment. It's a smaller,
// simpler, and more efficient regex, to quickly filter only generated comments and retrieve
// just their GitHub ID for matching.
var jCommentIDRegex = regexp.MustCompile("^Comment \\(ID (\\d+)\\)")

// CreateComments takes a GitHub issue, and retrieves all of its comments. It then
// matches each one to a comment in `existing`. If it finds a match, it calls
// UpdateComment; if it doesn't, it calls CreateComment.
func CompareComments(config cfg.Config, ghIssue github.Issue, jIssue jira.Issue, existing []jira.Comment, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	if *ghIssue.Comments == 0 {
		log.Debugf("Issue #%d has no comments, skipping.", *ghIssue.Number)
		return nil
	}

	ctx := context.Background()
	user, repo := config.GetRepo()
	c, _, err := cli.MakeGHRequest(config, func() (interface{}, *github.Response, error) {
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
		return errors.New(fmt.Sprintf("Get GitHub comments failed: expected []*github.IssueComment; got %T", c))
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

			UpdateComment(config, *ghComment, jComment, jIssue, ghClient, jClient)
			break
		}
		if found {
			continue
		}

		if err := CreateComment(config, *ghComment, jIssue, ghClient, jClient); err != nil {
			return err
		}
	}

	log.Debugf("Copied comments from GH issue #%d to JIRA issue %s.", *ghIssue.Number, jIssue.Key)
	return nil
}

// UpdateComment compares the body of a GitHub comment with the body (minus header)
// of the JIRA comment, and updates the JIRA comment if necessary.
func UpdateComment(config cfg.Config, ghComment github.IssueComment, jComment jira.Comment, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	// fields[0] is the whole body, 1 is the ID, 2 is the username, 3 is the real name (or "" if none)
	// 4 is the date, and 5 is the real body
	fields := jCommentRegex.FindStringSubmatch(jComment.Body)

	if fields[5] == *ghComment.Body {
		return nil
	}

	u, _, err := cli.MakeGHRequest(config, func() (interface{}, *github.Response, error) {
		return ghClient.Users.Get(context.Background(), *ghComment.User.Login)
	})
	if err != nil {
		log.Errorf("Error retrieving GitHub user %s. Error: %v", *ghComment.User.Login, err)
	}
	user, ok := u.(*github.User)
	if !ok {
		log.Errorf("Get GitHub user did not return user! Got: %v", u)
		return errors.New(fmt.Sprintf("Get GitHub user failed: expected *github.User; got %T", u))
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
			log.Errorf("Error creating comment update request: %s", err)
			return err
		}

		_, res, err := cli.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
			res, err := jClient.Do(req, nil)
			return nil, res, err
		})
		if err != nil {
			log.Errorf("Error updating comment: %v", err)
			return cli.GetErrorBody(config, res)
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

// CreateComment uses the ID, poster username, poster name, created at time, and body
// of a GitHub comment to generate the body of a JIRA comment, then creates it in the
// API.
func CreateComment(config cfg.Config, ghComment github.IssueComment, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
	log := config.GetLogger()

	u, _, err := cli.MakeGHRequest(config, func() (interface{}, *github.Response, error) {
		return ghClient.Users.Get(context.Background(), *ghComment.User.Login)
	})
	if err != nil {
		log.Errorf("Error retrieving GitHub user %s. Error: %v", *ghComment.User.Login, err)
		return err
	}
	user, ok := u.(*github.User)
	if !ok {
		log.Errorf("Get GitHub user did not return user! Got: %v", u)
		return errors.New(fmt.Sprintf("Get GitHub user failed: expected *github.User; got %T", u))
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
		_, res, err := cli.MakeJIRARequest(config, func() (interface{}, *jira.Response, error) {
			return jClient.Issue.AddComment(jIssue.ID, jComment)
		})
		if err != nil {
			log.Errorf("Error creating JIRA comment on issue %s. Error: %v", jIssue.Key, err)
			return cli.GetErrorBody(config, res)
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
