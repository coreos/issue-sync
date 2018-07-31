package lib

import (
	"strings"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/coreos/issue-sync/cfg"
	"github.com/coreos/issue-sync/lib/clients"
	"github.com/google/go-github/github"
)

// dateFormat is the format used for the Last IS Update field
const dateFormat = "2006-01-02T15:04:05.0-0700"

// CompareIssues gets the list of GitHub issues updated since the `since` date,
// gets the list of JIRA issues which have GitHub ID custom fields in that list,
// then matches each one. If a JIRA issue already exists for a given GitHub issue,
// it calls UpdateIssue; if no JIRA issue already exists, it calls CreateIssue.
func CompareIssues(config cfg.Config, ghClient clients.GitHubClient, jiraClient clients.JIRAClient) error {
	log := config.GetLogger()

	log.Debug("Collecting issues")

	ghIssues, err := ghClient.ListIssues()
	if err != nil {
		return err
	}

	if len(ghIssues) == 0 {
		log.Info("There are no GitHub issues; exiting")
		return nil
	}

	ids := make([]int, len(ghIssues))
	for i, v := range ghIssues {
		ids[i] = v.GetID()
	}

	jiraIssues, err := jiraClient.ListIssues(ids)
	if err != nil {
		return err
	}

	log.Debug("Collected all JIRA issues")

	for _, ghIssue := range ghIssues {
		found := false
		for _, jIssue := range jiraIssues {
			id, _ := jIssue.Fields.Unknowns.Int(config.GetFieldKey(cfg.GitHubID))
			if int64(*ghIssue.ID) == id {
				found = true
				if err := UpdateIssue(config, ghIssue, jIssue, ghClient, jiraClient); err != nil {
					log.Errorf("Error updating issue %s. Error: %v", jIssue.Key, err)
				}
				break
			}
		}
		if !found {
			if err := CreateIssue(config, ghIssue, ghClient, jiraClient); err != nil {
				log.Errorf("Error creating issue for #%d. Error: %v", *ghIssue.Number, err)
			}
		}
	}

	return nil
}

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
func UpdateIssue(config cfg.Config, ghIssue github.Issue, jIssue jira.Issue, ghClient clients.GitHubClient, jClient clients.JIRAClient) error {
	log := config.GetLogger()

	log.Debugf("Updating JIRA %s with GitHub #%d", jIssue.Key, *ghIssue.Number)

	var issue jira.Issue

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

		issue = jira.Issue{
			Fields: &fields,
			Key:    jIssue.Key,
			ID:     jIssue.ID,
		}

		var err error
		issue, err = jClient.UpdateIssue(issue)
		if err != nil {
			return err
		}

		log.Debugf("Successfully updated JIRA issue %s!", jIssue.Key)
	} else {
		log.Debugf("JIRA issue %s is already up to date!", jIssue.Key)
	}

	issue, err := jClient.GetIssue(jIssue.Key)
	if err != nil {
		log.Debugf("Failed to retrieve JIRA issue %s!", jIssue.Key)
		return err
	}

	if err := CompareComments(config, ghIssue, issue, ghClient, jClient); err != nil {
		return err
	}

	return nil
}

// CreateIssue generates a JIRA issue from the various fields on the given GitHub issue, then
// sends it to the JIRA API.
func CreateIssue(config cfg.Config, issue github.Issue, ghClient clients.GitHubClient, jClient clients.JIRAClient) error {
	log := config.GetLogger()

	log.Debugf("Creating JIRA issue based on GitHub issue #%d", *issue.Number)

	fields := jira.IssueFields{
		Type: jira.IssueType{
			Name: "Task", // TODO: Determine issue type
		},
		Project:     config.GetProject(),
		Summary:     issue.GetTitle(),
		Description: issue.GetBody(),
		Unknowns:    map[string]interface{}{},
	}

	fields.Unknowns[config.GetFieldKey(cfg.GitHubID)] = issue.GetID()
	fields.Unknowns[config.GetFieldKey(cfg.GitHubNumber)] = issue.GetNumber()
	fields.Unknowns[config.GetFieldKey(cfg.GitHubStatus)] = issue.GetState()
	fields.Unknowns[config.GetFieldKey(cfg.GitHubReporter)] = issue.User.GetLogin()

	strs := make([]string, len(issue.Labels))
	for i, v := range issue.Labels {
		strs[i] = *v.Name
	}
	fields.Unknowns[config.GetFieldKey(cfg.GitHubLabels)] = strings.Join(strs, ",")

	fields.Unknowns[config.GetFieldKey(cfg.LastISUpdate)] = time.Now().Format(dateFormat)

	jIssue := jira.Issue{
		Fields: &fields,
	}

	jIssue, err := jClient.CreateIssue(jIssue)
	if err != nil {
		return err
	}

	jIssue, err = jClient.GetIssue(jIssue.Key)
	if err != nil {
		return err
	}

	log.Debugf("Created JIRA issue %s!", jIssue.Key)

	if err := CompareComments(config, issue, jIssue, ghClient, jClient); err != nil {
		return err
	}

	return nil
}
