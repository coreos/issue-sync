package cfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/andygrunwald/go-jira"
	"github.com/coreos/issue-sync/lib/models"
	"strings"
	"time"
)

type FieldMapper interface {
	MapFields(issue *models.ExtendedGithubIssue) jira.IssueFields

	GetFieldValue(jIssue *jira.Issue, fieldKey FieldKey) (interface{}, error)

	// getFieldIDs requests the metadata of every issue field in the JIRA
	// project, and saves the IDs of the custom fields used by issue-sync.
	GetFieldIDs(client jira.Client) (map[FieldKey]string, error)
}

type DefaultFieldMapper struct {
	Config *Config
}

type JsonFieldMapper struct {
	Config *Config
}

func (m DefaultFieldMapper) GetFieldValue(jIssue *jira.Issue, fieldKey FieldKey) (interface{}, error) {
	switch fieldKey {
	case GitHubID:
		fallthrough
	case GitHubNumber:
		return jIssue.Fields.Unknowns.Int(m.Config.GetCompleteFieldKey(fieldKey))
	default:
		result, exists := jIssue.Fields.Unknowns.Value(m.Config.GetCompleteFieldKey(fieldKey))
		if !exists {
			return nil, errors.New("Field not found")
		}
		return result, nil
	}
}

func (m DefaultFieldMapper) MapFields(issue *models.ExtendedGithubIssue) jira.IssueFields {
	fields := jira.IssueFields{
		Type: jira.IssueType{
			Name: "Task", // TODO: Determine issue type
		},
		Project:     m.Config.GetProject(),
		Summary:     issue.GetTitle(),
		Description: issue.GetBody(),
		Unknowns:    map[string]interface{}{},
	}

	fields.Unknowns[m.Config.GetCompleteFieldKey(GitHubID)] = issue.GetID()
	fields.Unknowns[m.Config.GetCompleteFieldKey(GitHubNumber)] = issue.GetNumber()
	fields.Unknowns[m.Config.GetCompleteFieldKey(GitHubStatus)] = issue.GetState()
	fields.Unknowns[m.Config.GetCompleteFieldKey(GitHubReporter)] = issue.User.GetLogin()

	githubLabels := make([]string, len(issue.Labels))
	for i, v := range issue.Labels {
		githubLabels[i] = *v.Name
	}
	fields.Unknowns[m.Config.GetCompleteFieldKey(GitHubLabels)] = strings.Join(githubLabels, ",")

	fields.Unknowns[m.Config.GetCompleteFieldKey(LastISUpdate)] = time.Now().Format(DateFormat)

	return fields
}

func (m DefaultFieldMapper) GetFieldIDs(client jira.Client) (map[FieldKey]string, error) {
	m.Config.log.Debug("Collecting field IDs.")
	req, err := client.NewRequest("GET", "/rest/api/2/field", nil)
	if err != nil {
		return map[FieldKey]string{}, err
	}
	jFields := new([]jiraField)

	_, err = client.Do(req, jFields)
	if err != nil {
		return map[FieldKey]string{}, err
	}

	fieldIDs := map[FieldKey]string{}

	for _, field := range *jFields {
		switch field.Name {
		case "GitHub ID":
			fieldIDs[GitHubID] = fmt.Sprint(field.Schema.CustomID)
		case "GitHub Number":
			fieldIDs[GitHubNumber] = fmt.Sprint(field.Schema.CustomID)
		case "GitHub Labels":
			fieldIDs[GitHubLabels] = fmt.Sprint(field.Schema.CustomID)
		case "GitHub Status":
			fieldIDs[GitHubStatus] = fmt.Sprint(field.Schema.CustomID)
		case "GitHub Reporter":
			fieldIDs[GitHubReporter] = fmt.Sprint(field.Schema.CustomID)
		case "Last Issue-Sync Update":
			fieldIDs[LastISUpdate] = fmt.Sprint(field.Schema.CustomID)
		}
	}

	_, ok := fieldIDs[GitHubID]
	if !ok {
		return fieldIDs, errors.New("could not find ID of 'GitHub ID' custom field; check that it is named correctly")
	}

	_, ok = fieldIDs[GitHubNumber]
	if !ok {
		return fieldIDs, errors.New("could not find ID of 'GitHub Number' custom field; check that it is named correctly")
	}

	_, ok = fieldIDs[GitHubLabels]
	if !ok {
		return fieldIDs, errors.New("could not find ID of 'Github Labels' custom field; check that it is named correctly")
	}

	_, ok = fieldIDs[GitHubStatus]
	if !ok {
		return fieldIDs, errors.New("could not find ID of 'Github Status' custom field; check that it is named correctly")
	}

	_, ok = fieldIDs[GitHubReporter]
	if !ok {
		return fieldIDs, errors.New("could not find ID of 'Github Reporter' custom field; check that it is named correctly")
	}

	_, ok = fieldIDs[LastISUpdate]
	if !ok  {
		return fieldIDs, errors.New("could not find ID of 'Last Issue-Sync Update' custom field; check that it is named correctly")
	}

	m.Config.log.Debug("All fields have been checked.")

	return fieldIDs, nil
}

func (m JsonFieldMapper) GetFieldValue(jIssue *jira.Issue, fieldKey FieldKey) (interface{}, error) {

	var result interface{}

	jsonGithubData, err := jIssue.Fields.Unknowns.String(m.Config.GetCompleteFieldKey(GitHubIssueData))
	if  err != nil {
		return nil, err
	}

	var parsedJson map[string]interface{}
	err = json.Unmarshal([]byte(jsonGithubData), &parsedJson)
	if  err != nil {
		return nil, err
	}

	switch fieldKey {
	case GitHubID:
		result = int64(parsedJson["githubId"].(float64))
	case GitHubNumber:
		result = int64(parsedJson["githubNumber"].(float64))
	case GitHubLabels:
		result = parsedJson["githubLabels"]
	case GitHubStatus:
		result = parsedJson["githubStatus"]
	case GitHubReporter:
		result = parsedJson["githubReporter"]
	case GitHubCommits:
		result = parsedJson["githubCommits"]
		if result == nil {
			result = make([]interface{}, 0)
		}
	case LastISUpdate:
		result = parsedJson["lastIssueSyncUpdate"]
	}
	return result, nil
}

func (m JsonFieldMapper) MapFields(issue *models.ExtendedGithubIssue) jira.IssueFields {
	fields := jira.IssueFields{
		Type: jira.IssueType{
			Name: "Task", // TODO: Determine issue type
		},
		Project:     m.Config.GetProject(),
		Summary:     issue.GetTitle(),
		Description: issue.GetBody(),
		Unknowns:    map[string]interface{}{},
	}

	githubLabels := make([]string, len(issue.Labels))
	for i, v := range issue.Labels {
		githubLabels[i] = *v.Name
	}

	data := map[string]interface{}{
		"githubId": issue.GetID(),
		"githubNumber": issue.GetNumber(),
		"githubStatus":issue.GetState(),
		"githubReporter": issue.User.GetLogin(),
		"githubLabels": strings.Join(githubLabels, ","),
		"githubCommits": issue.CommitIds,
		"lastIssueSyncUpdate": time.Now().Format(DateFormat),
	}

	j, _ := json.Marshal(data)
	fields.Unknowns[m.Config.GetCompleteFieldKey(GitHubIssueData)] = string(j)

	return fields
}

func (m JsonFieldMapper) GetFieldIDs(client jira.Client) (map[FieldKey]string, error) {
	m.Config.log.Debug("Collecting field IDs.")
	req, err := client.NewRequest("GET", "/rest/api/2/field", nil)
	if err != nil {
		return map[FieldKey]string{}, err
	}
	jFields := new([]jiraField)

	_, err = client.Do(req, jFields)
	if err != nil {
		return map[FieldKey]string{}, err
	}

	fieldIDs := map[FieldKey]string{}

	for _, field := range *jFields {
		switch field.Name {
		case "GitHub Issue Data":
			fieldIDs[GitHubIssueData] = fmt.Sprint(field.Schema.CustomID)
		}
	}

	_, ok := fieldIDs[GitHubIssueData]
	if !ok {
		return fieldIDs, errors.New("could not find ID of 'GitHub Issue Data' custom field; check that it is named correctly")
	}

	m.Config.log.Debug("All fields have been checked.")

	return fieldIDs, nil
}