package lib

import (
	"fmt"
	"github.com/andygrunwald/go-jira"
	"github.com/coreos/issue-sync/cfg"
	"github.com/coreos/issue-sync/lib/issuesyncgithub"
	"github.com/coreos/issue-sync/lib/issuesyncjira"
	"github.com/coreos/issue-sync/lib/models"
	"strings"
)

// CompareIssues gets the list of GitHub issues updated since the `since` date,
// gets the list of JIRA issues which have GitHub ID custom fields in that list,
// then matches each one. If a JIRA issue already exists for a given GitHub issue,
// it calls UpdateIssue; if no JIRA issue already exists, it calls CreateIssue.
func CompareIssues(config cfg.Config, ghClient issuesyncgithub.Client, jiraClient issuesyncjira.Client) error {
	log := config.GetLogger()
	user, repoName := config.GetRepo()

	log.Debug("Collecting issues")

	ghIssues, err := issuesyncgithub.ListIssues(ghClient, log, config.GetTimeout(), user, repoName, config.GetSinceParam())
	if err != nil {
		return err
	}

	if len(ghIssues) == 0 {
		log.Info("There are no GitHub issues; exiting")
		return nil
	}

	ids := make([]int64, len(ghIssues))
	for i, v := range ghIssues {
		ids[i] = v.GetID()
	}

	jiraIssues, err := issuesyncjira.ListIssues(jiraClient, ids)
	if err != nil {
		return err
	}

	log.Debug("Collected all JIRA issues")

	for _, ghIssue := range ghIssues {
		found := false
		for _, jIssue := range jiraIssues {

			id, _ := config.GetFieldMapper().GetFieldValue(&jIssue, cfg.GitHubID)
			if int64(*ghIssue.ID) == id.(int64) {
				found = true
				if err := UpdateIssue(config, ghIssue, jIssue, ghClient, jiraClient,); err != nil {
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

func jiraCustomFieldsNeedUpdate(config cfg.Config, jIssue jira.Issue, fieldKey cfg.FieldKey, githubFieldValue string) bool {
	jiraField, err := config.GetFieldMapper().GetFieldValue(&jIssue, fieldKey)

	// update if there was an error retrieving the value
	if err != nil {
		return true
	}

	if jiraField == nil {
		// treat empty string and nil as equal
		return len(githubFieldValue) != 0
	} else {
		// update if there is any difference between actual values
		return jiraField.(string) != githubFieldValue
	}
}

func sliceStringsEq(a []string, b []string) bool {

	if (a == nil) != (b == nil) {
		return false;
	}

	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// DidIssueChange tests each of the relevant fields on the provided JIRA and GitHub issue
// and returns whether or not they differ.
func DidIssueChange(config cfg.Config, ghIssue models.ExtendedGithubIssue, jIssue jira.Issue) bool {
	log := config.GetLogger()

	log.Debugf("Comparing GitHub issue #%d and JIRA issue %s", ghIssue.GetNumber(), jIssue.Key)

	anyDifferent := false

	anyDifferent = anyDifferent || (ghIssue.GetTitle() != jIssue.Fields.Summary)
	anyDifferent = anyDifferent || (ghIssue.GetBody() != jIssue.Fields.Description)
	anyDifferent = anyDifferent || jiraCustomFieldsNeedUpdate(config, jIssue, cfg.GitHubStatus, ghIssue.GetState())
	anyDifferent = anyDifferent || jiraCustomFieldsNeedUpdate(config, jIssue, cfg.GitHubReporter, ghIssue.User.GetLogin())
	commits, err := config.GetFieldMapper().GetFieldValue(&jIssue, cfg.GitHubCommits)
	commitIdsInJiraUntyped := commits.([]interface{})

	if err != nil {
		return true
	}

	commitIdsInJira := make([]string, len(commitIdsInJiraUntyped))
	for i, v := range commitIdsInJiraUntyped {
		commitIdsInJira[i] = fmt.Sprint(v)
	}

	anyDifferent = anyDifferent || sliceStringsEq(commitIdsInJira, ghIssue.CommitIds)
	ghLabels := make([]string, len(ghIssue.Labels))
	for i, l := range ghIssue.Labels {
		ghLabels[i] = *l.Name
	}
	ghLabelsString := strings.Join(ghLabels, ",")

	anyDifferent = anyDifferent || jiraCustomFieldsNeedUpdate(config, jIssue, cfg.GitHubLabels, ghLabelsString)
	anyDifferent = anyDifferent || (ghIssue.ProjectCard != nil && strings.ToLower(jIssue.Fields.Status.Name) != strings.ToLower(ghIssue.ProjectCard.GetColumnName()))
	log.Debugf("Issues have any differences: %t", anyDifferent)

	return anyDifferent
}

// UpdateIssue compares each field of a GitHub issue to a JIRA issue; if any of them
// differ, the differing fields of the JIRA issue are updated to match the GitHub
// issue.
func UpdateIssue(config cfg.Config, ghIssue models.ExtendedGithubIssue, jIssue jira.Issue, ghClient issuesyncgithub.Client, jClient issuesyncjira.Client) error {
	log := config.GetLogger()

	log.Debugf("Updating JIRA %s with GitHub #%d", jIssue.Key, *ghIssue.Number)

	var issue jira.Issue

	if DidIssueChange(config, ghIssue, jIssue) {
		fields := config.GetFieldMapper().MapFields(&ghIssue)

		issue = jira.Issue{
			Fields: &fields,
			Key:    jIssue.Key,
			ID:     jIssue.ID,
		}

		var err error

		if ghIssue.ProjectCard != nil {
			err = issuesyncjira.TryApplyTransitionWithName(jClient, jIssue, ghIssue.ProjectCard.GetColumnName())
			if err != nil {
				return err
			}
		}

		issue, err = issuesyncjira.UpdateIssue(jClient, issue)
		if err != nil {
			return err
		}

		log.Debugf("Successfully updated JIRA issue %s!", jIssue.Key)
	} else {
		log.Debugf("JIRA issue %s is already up to date!", jIssue.Key)
	}

	issue, err := issuesyncjira.GetIssue(jClient, jIssue.Key)
	if err != nil {
		log.Debugf("Failed to retrieve JIRA issue %s!", jIssue.Key)
		return err
	}

	if err := CompareComments(config, ghIssue.Issue, issue, ghClient, jClient); err != nil {
		return err
	}

	return nil
}

// CreateIssue generates a JIRA issue from the various fields on the given GitHub issue, then
// sends it to the JIRA API.
func CreateIssue(config cfg.Config, ghIssue models.ExtendedGithubIssue, ghClient issuesyncgithub.Client, jClient issuesyncjira.Client) error {
	log := config.GetLogger()

	log.Debugf("Creating JIRA issue based on GitHub issue #%d", *ghIssue.Issue.Number)

	fields := config.GetFieldMapper().MapFields(&ghIssue)

	jIssue := jira.Issue{
		Fields: &fields,
	}

	jIssue, err := issuesyncjira.CreateIssue(jClient, jIssue)
	if err != nil {
		return err
	}

	if ghIssue.ProjectCard != nil {
		err = issuesyncjira.TryApplyTransitionWithName(jClient, jIssue, ghIssue.ProjectCard.GetColumnName())
		if err != nil {
			return err
		}
	}

	jIssue, err = issuesyncjira.GetIssue(jClient, jIssue.Key)
	if err != nil {
		return err
	}

	log.Debugf("Created JIRA issue %s!", jIssue.Key)

	if err := CompareComments(config, ghIssue.Issue, jIssue, ghClient, jClient); err != nil {
		return err
	}

	return nil
}
