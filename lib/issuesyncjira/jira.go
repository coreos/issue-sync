package issuesyncjira

import (
	"errors"
	"fmt"
	"github.com/coreos/issue-sync/lib/issuesyncgithub"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"

	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/cenkalti/backoff"
	"github.com/coreos/issue-sync/cfg"
	"github.com/coreos/issue-sync/lib/utils"
	"github.com/google/go-github/github"
)

// commentDateFormat is the format used in the headers of JIRA comments.
const commentDateFormat = "15:04 PM, January 2 2006"

// maxJQLIssueLength is the maximum number of GitHub issues we can
// use before we need to stop using JQL and filter issues ourself.
const maxJQLIssueLength = 100

// getErrorBody reads the HTTP response body of a JIRA API response,
// logs it as an error, and returns an error object with the contents
// of the body. If an error occurs during reading, that error is
// instead printed and returned. This function closes the body for
// further reading.
func getErrorBody(config cfg.Config, res *jira.Response) error {
	log := config.GetLogger()
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Errorf("Error occured trying to read error body: %v", err)
		return err
	}
	log.Debugf("Error body: %s", body)
	return errors.New(string(body))
}

// Client is a wrapper around the JIRA API clients library we
// use. It allows us to hide implementation details such as backoff
// as well as swap in other implementations, such as for dry run
// or test mocking.
type Client interface {
	getConfig() cfg.Config
	searchIssues(jql string) (interface{}, *jira.Response, error)
	do(method string, url string, body interface{}, out interface{}) (*jira.Response, error)
	getIssue(key string) (*jira.Issue, *jira.Response, error)
	createIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error)
	updateIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error)
	addComment(id string, jComment *jira.Comment, jIssue *jira.Issue, ghComment *github.IssueComment, ghUser *github.User) (*jira.Comment, *jira.Response, error)
	getTransitions(issue jira.Issue) ([]jira.Transition, *jira.Response, error)
	applyTransition(issue jira.Issue, transition jira.Transition) (*jira.Response, error)
}

// realJIRAClient is a standard JIRA clients, which actually makes
// of the requests against the JIRA REST API. It is the canonical
// implementation of Client.
type realJIRAClient struct {
	config cfg.Config
	client jira.Client
}

// dryrunJIRAClient is an implementation of Client which performs all
// GET requests the same as the realJIRAClient, but does not perform any
// unsafe requests which may modify server data, instead printing out the
// actions it is asked to perform without making the request.
type dryrunJIRAClient struct {
	config cfg.Config
	client jira.Client
}

func (j realJIRAClient) getConfig() cfg.Config {
	return j.config
}

func (j realJIRAClient) getTransitions(issue jira.Issue) ([]jira.Transition, *jira.Response, error) {
	return j.client.Issue.GetTransitions(issue.ID)
}

func (j realJIRAClient) applyTransition(issue jira.Issue, transition jira.Transition) (*jira.Response, error) {
	return j.client.Issue.DoTransition(issue.ID, transition.ID)
}

func (j realJIRAClient) searchIssues(jql string) (interface{}, *jira.Response, error) {
	return j.client.Issue.Search(jql, nil)
}

func (j realJIRAClient) getIssue(key string) (*jira.Issue, *jira.Response, error) {
	return j.client.Issue.Get(key, nil)
}

func (j realJIRAClient) createIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error) {
	return j.client.Issue.Create(issue)
}

func (j realJIRAClient) updateIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error) {
	return j.client.Issue.Update(issue)
}

func (j realJIRAClient) addComment(id string, jComment *jira.Comment, jIssue *jira.Issue, ghComment *github.IssueComment, ghUser *github.User) (*jira.Comment, *jira.Response, error) {
	return j.client.Issue.AddComment(id, jComment)
}

func (j realJIRAClient) do(method string, url string, body interface{}, out interface{}) (*jira.Response, error) {
	req, _ := j.client.NewRequest(method, url, body)
	return j.client.Do(req, out)
}

// DRY RUN CLIENT

func (j dryrunJIRAClient) getTransitions(issue jira.Issue) ([]jira.Transition, *jira.Response, error) {
	return j.client.Issue.GetTransitions(issue.ID)
}

func (j dryrunJIRAClient) applyTransition(issue jira.Issue, transition jira.Transition) (*jira.Response, error) {
	log := j.config.GetLogger()
	log.Info("")
	log.Info("Applying Transition:")
	log.Infof("  Jira Issue ID: %s", issue.ID)
	log.Infof("  Transition Id: %s", transition.ID)
	log.Infof("  Old Status: %s", issue.Fields.Status.Name)
	log.Infof("  New Satus: %s", transition.To.Name)
	log.Info("")
	return nil, nil
}

func (j dryrunJIRAClient) getConfig() cfg.Config {
	return j.config
}

func (j dryrunJIRAClient) searchIssues(jql string) (interface{}, *jira.Response, error) {
	return j.client.Issue.Search(jql, nil)
}

func (j dryrunJIRAClient) getIssue(key string) (*jira.Issue, *jira.Response, error) {
	return j.client.Issue.Get(key, nil)
}

func (j dryrunJIRAClient) createIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error) {
	log := j.config.GetLogger()

	fields := issue.Fields

	log.Info("")
	log.Info("Create new JIRA issue:")
	log.Infof("  Summary: %s", fields.Summary)
	log.Infof("  Description: %s", truncate(fields.Description, 50))
	log.Infof("  GitHub ID: %d", utils.GetOrElse(j.config.GetFieldMapper().GetFieldValue(issue, cfg.GitHubID))(""))
	log.Infof("  GitHub Number: %d", utils.GetOrElse(j.config.GetFieldMapper().GetFieldValue(issue, cfg.GitHubNumber)))
	log.Infof("  GitHub Labels: %s", utils.GetOrElse(j.config.GetFieldMapper().GetFieldValue(issue, cfg.GitHubLabels)))
	log.Infof("  GitHub Status: %s", utils.GetOrElse(j.config.GetFieldMapper().GetFieldValue(issue, cfg.GitHubStatus)))
	log.Infof("  GitHub Reporter: %s", utils.GetOrElse(j.config.GetFieldMapper().GetFieldValue(issue, cfg.GitHubReporter)))
	log.Info("")

	return issue, nil, nil
}

func (j dryrunJIRAClient) updateIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error) {
	log := j.config.GetLogger()

	fields := issue.Fields

	log.Info("")
	log.Infof("Update JIRA issue %s:", issue.Key)
	log.Infof("  Summary: %s", fields.Summary)
	log.Infof("  Description: %s", truncate(fields.Description, 50))
	if labels, err := j.config.GetFieldMapper().GetFieldValue(issue, cfg.GitHubLabels); err == nil {
		log.Infof("  Labels: %s", labels)
	}
	if state, err := j.config.GetFieldMapper().GetFieldValue(issue, cfg.GitHubStatus); err == nil {
		log.Infof("  State: %s", state)
	}
	log.Info("")

	return issue, nil, nil
}

func (j dryrunJIRAClient) addComment(id string, jComment *jira.Comment, jIssue *jira.Issue, ghComment *github.IssueComment, ghUser *github.User) (*jira.Comment, *jira.Response, error) {
	log := j.config.GetLogger()

	body := fmt.Sprintf("Comment (ID %d) from GitHub user %s", ghComment.GetID(), ghUser.GetLogin())
	if ghUser.GetName() != "" {
		body = fmt.Sprintf("%s (%s)", body, ghUser.GetName())
	}
	body = fmt.Sprintf(
		"%s at %s:\n\n%s",
		body,
		ghComment.CreatedAt.Format(commentDateFormat),
		ghComment.GetBody(),
	)

	log.Info("")
	log.Infof("Create comment on JIRA issue %s:", jIssue.Key)
	log.Infof("  GitHub ID: %d", ghComment.GetID())
	if ghUser.GetName() != "" {
		log.Infof("  User: %s (%s)", ghUser.GetLogin(), ghUser.GetName())
	} else {
		log.Infof("  User: %s", ghUser.GetLogin())
	}
	log.Infof("  Posted at: %s", ghComment.CreatedAt.Format(commentDateFormat))
	log.Infof("  Body: %s", truncate(ghComment.GetBody(), 100))
	log.Info("")

	return &jira.Comment{
		Body: body,
	}, nil, nil
}

func (j dryrunJIRAClient) do(method string, url string, body interface{}, out interface{}) (*jira.Response, error) {
	disallowedMethods := map[string]bool{"POST": true, "PUT": true}

	if disallowedMethods[method] {
		return nil, nil
	}
	req, _ := j.client.NewRequest(method, url, nil)
	return j.client.Do(req, out)
}



const retryBackoffRoundRatio = time.Millisecond / time.Nanosecond

// request takes an API function from the JIRA library
// and calls it with exponential backoff. If the function succeeds, it
// returns the expected value and the JIRA API response, as well as a nil
// error. If it continues to fail until a maximum time is reached, it returns
// a nil result as well as the returned HTTP response and a timeout error.
func request(config cfg.Config, f func() (interface{}, *jira.Response, error)) (interface{}, *jira.Response, error) {
	log := config.GetLogger()

	var ret interface{}
	var res *jira.Response

	op := func() error {
		var err error
		ret, res, err = f()
		return err
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = config.GetTimeout()

	backoffErr := backoff.RetryNotify(op, b, func(err error, duration time.Duration) {
		// Round to a whole number of milliseconds
		duration /= retryBackoffRoundRatio // Convert nanoseconds to milliseconds
		duration *= retryBackoffRoundRatio // Convert back so it appears correct

		log.Errorf("Error performing operation; retrying in %v: %v", duration, err)
	})

	return ret, res, backoffErr
}

// NewClient creates a new Client and configures it with
// the config object provided. The type of clients created depends
// on the configuration; currently, it creates either a standard
// clients, or a dry-run clients.
func NewClient(config *cfg.Config) (Client, error) {
	log := config.GetLogger()

	var oauth *http.Client
	var err error
	if !config.IsBasicAuth() {
		oauth, err = newJIRAHTTPClient(*config)
		if err != nil {
			log.Errorf("Error getting OAuth config: %v", err)
			return dryrunJIRAClient{}, err
		}
	}

	var j Client

	client, err := jira.NewClient(oauth, config.GetConfigString("jira-uri"))
	if err != nil {
		log.Errorf("Error initializing JIRA clients; check your base URI. Error: %v", err)
		return dryrunJIRAClient{}, err
	}

	if config.IsBasicAuth() {
		client.Authentication.SetBasicAuth(config.GetConfigString("jira-user"), config.GetConfigString("jira-pass"))
	}

	log.Debug("JIRA clients initialized")

	err = config.LoadJIRAConfig(*client)

	if err != nil {
		log.Errorf("Error loading config", err)
		return dryrunJIRAClient{}, err
	}

	if config.IsDryRun() {
		j = dryrunJIRAClient{
			config: *config,
			client: *client,
		}
	} else {
		j = realJIRAClient{
			config: *config,
			client: *client,
		}
	}

	return j, nil
}

func TryApplyTransitionWithName(j Client, issue jira.Issue, statusName string) error {
	log := j.getConfig().GetLogger()


	var currentStatusName string
	if issue.Fields == nil || issue.Fields.Status == nil {
		currentStatusName = ""
	} else {
		currentStatusName = strings.ToLower(issue.Fields.Status.Name)
	}

	targetStatusName := strings.ToLower(statusName)
	if currentStatusName == targetStatusName {
		log.Debug("Issue Status is already in sync")
		return nil
	}

	transitions, res, err := j.getTransitions(issue)

	if err != nil {
		log.Errorf("Error retrieving JIRA transitions: %v", err)
		return getErrorBody(j.getConfig(), res)
	}

	for _, v := range transitions {
		if strings.ToLower(v.To.Name) == targetStatusName {
			log.Info(fmt.Sprintf("Applying transition %s -> %s on issue %s", currentStatusName, v.To.Name, issue.ID))
			_, err = j.applyTransition(issue, v)
			if err != nil {
				log.Errorf("Error applying JIRA transitions: %v", err)
				return getErrorBody(j.getConfig(), res)
			} else {
				return nil
			}
		}
	}
	// TODO: This is where we can decide what to do about invalid transitions
	return errors.New(fmt.Sprintf("No transition from '%s' to '%s' found for issue %s", currentStatusName, statusName, issue.ID))
}

// ListIssues returns a list of JIRA issues on the configured project which
// have GitHub IDs in the provided list. `ids` should be a comma-separated
// list of GitHub IDs.
func ListIssues(j Client, ghIssueIds []int64) ([]jira.Issue, error) {
	log := j.getConfig().GetLogger()

	ghIssueIdStrs := make([]string, len(ghIssueIds))
	for i, v := range ghIssueIds {
		ghIssueIdStrs[i] = fmt.Sprint(v)
	}

	var jql string
	// If the list of IDs is too long, we get a 414 Request-URI Too Large, so in that case,
	// we'll need to do the filtering ourselves.
	if !j.getConfig().IsUsingGitHubIssueDataField() && len(ghIssueIds) < maxJQLIssueLength {
		jql = fmt.Sprintf("project='%s' AND cf[%s] in (%s)",
			j.getConfig().GetProjectKey(), j.getConfig().GetFieldID(cfg.GitHubID), strings.Join(ghIssueIdStrs, ","))
	} else {
		jql = fmt.Sprintf("project='%s'", j.getConfig().GetProjectKey())
	}

	ji, res, err := request(j.getConfig(), func() (interface{}, *jira.Response, error) {
		return j.searchIssues(jql)
	})

	if err != nil {
		log.Errorf("Error retrieving JIRA issues: %v", err)
		return nil, getErrorBody(j.getConfig(), res)
	}

	var jiraIssues []jira.Issue
	// simulating a set
	projectKeySet := make(map[string] bool)
	jis, ok := ji.([]jira.Issue)
	for _, v := range jis {
		projectKeySet[v.Fields.Project.Key] = true
	}

	projectKeys := make([]string, 0, len(projectKeySet))
	for k := range projectKeySet {
		projectKeys = append(projectKeys, k)
	}

	for _, v := range jis {
		jiraIssues = append(jiraIssues, v)
	}

	if !ok {
		log.Errorf("Get JIRA issues did not return issues! Got: %v", ji)
		return nil, fmt.Errorf("get JIRA issues failed: expected []jira.Issue; got %T", ji)
	}

	var issues []jira.Issue
	if !j.getConfig().IsUsingGitHubIssueDataField() && len(ghIssueIds) < maxJQLIssueLength {
		// The issues were already filtered by our JQL, so use as is
		issues = jiraIssues
	} else {
		// Filter only issues which have a defined GitHub ID in the list of IDs
		for _, v := range jiraIssues {
			if id, err := j.getConfig().GetFieldMapper().GetFieldValue(&v, cfg.GitHubID); err == nil {
				for _, idOpt := range ghIssueIds {
					if id.(int64) == int64(idOpt) {
						issues = append(issues, v)
						break
					}
				}
			}
		}
	}
	return issues, nil
}

// GetIssue returns a single JIRA issue within the configured project
// according to the issue key (e.g. "PROJ-13").
func GetIssue(j Client, key string) (jira.Issue, error) {
	log := j.getConfig().GetLogger()

	i, res, err := request(j.getConfig(), func() (interface{}, *jira.Response, error) {
		return j.getIssue(key)
	})
	if err != nil {
		log.Errorf("Error retrieving JIRA issue: %v", err)
		return jira.Issue{}, getErrorBody(j.getConfig(), res)
	}
	issue, ok := i.(*jira.Issue)
	if !ok {
		log.Errorf("Get JIRA issue did not return issue! Got %v", i)
		return jira.Issue{}, fmt.Errorf("Get JIRA issue failed: expected *jira.Issue; got %T", i)
	}

	return *issue, nil
}

// CreateIssue creates a new JIRA issue according to the fields provided in
// the provided issue object. It returns the created issue, with all the
// fields provided (including e.g. ID and Key).
func CreateIssue(j Client, issue jira.Issue) (jira.Issue, error) {
	log := j.getConfig().GetLogger()

	i, res, err := request(j.getConfig(), func() (interface{}, *jira.Response, error) {
		return j.createIssue(&issue)
	})
	if err != nil {
		log.Errorf("Error creating JIRA issue: %v", err)
		return jira.Issue{}, getErrorBody(j.getConfig(), res)
	}
	is, ok := i.(*jira.Issue)
	if !ok {
		log.Errorf("Create JIRA issue did not return issue! Got: %v", i)
		return jira.Issue{}, fmt.Errorf("Create JIRA issue failed: expected *jira.Issue; got %T", i)
	}

	return *is, nil
}

// UpdateIssue updates a given issue (identified by the Key field of the provided
// issue object) with the fields on the provided issue. It returns the updated
// issue as it exists on JIRA.
func UpdateIssue(j Client, issue jira.Issue) (jira.Issue, error) {
	log := j.getConfig().GetLogger()

	i, res, err := request(j.getConfig(), func() (interface{}, *jira.Response, error) {
		return j.updateIssue(&issue)
	})
	if err != nil {
		log.Errorf("Error updating JIRA issue %s: %v", issue.Key, err)
		return jira.Issue{}, getErrorBody(j.getConfig(), res)
	}
	is, ok := i.(*jira.Issue)
	if !ok {
		log.Errorf("Update JIRA issue did not return issue! Got: %v", i)
		return jira.Issue{}, fmt.Errorf("Update JIRA issue failed: expected *jira.Issue; got %T", i)
	}

	return *is, nil
}

// maxBodyLength is the maximum length of a JIRA comment body, which is currently
// 2^15-1.
const maxBodyLength = 1 << 15

// CreateComment adds a comment to the provided JIRA issue using the fields from
// the provided GitHub comment. It then returns the created comment.
func CreateComment(j Client, jIssue jira.Issue, ghComment github.IssueComment, g issuesyncgithub.Client) (jira.Comment, error) {
	config := j.getConfig()
	log := config.GetLogger()

	user, err := issuesyncgithub.GetUser(g, log, config.GetTimeout(), ghComment.User.GetLogin())
	if err != nil {
		return jira.Comment{}, err
	}

	body := fmt.Sprintf("Comment [(ID %d)|%s]", ghComment.GetID(), ghComment.GetHTMLURL())
	body = fmt.Sprintf("%s from GitHub user [%s|%s]", body, user.GetLogin(), user.GetHTMLURL())
	if user.GetName() != "" {
		body = fmt.Sprintf("%s (%s)", body, user.GetName())
	}
	body = fmt.Sprintf(
		"%s at %s:\n\n%s",
		body,
		ghComment.CreatedAt.Format(commentDateFormat),
		ghComment.GetBody(),
	)

	if len(body) > maxBodyLength {
		body = body[:maxBodyLength]
	}

	jComment := jira.Comment{
		Body: body,
	}

	ghUser, err := issuesyncgithub.GetUser(g, log, config.GetTimeout(), ghComment.User.GetLogin())
	if err != nil {
		return jira.Comment{}, err
	}

	com, res, err := request(j.getConfig(), func() (interface{}, *jira.Response, error) {
		return j.addComment(jIssue.ID, &jComment, &jIssue, &ghComment, &ghUser)
	})
	if err != nil {
		log.Errorf("Error creating JIRA ghComment on jIssue %s. Error: %v", jIssue.Key, err)
		return jira.Comment{}, getErrorBody(j.getConfig(), res)
	}
	co, ok := com.(*jira.Comment)
	if !ok {
		log.Errorf("Create JIRA ghComment did not return ghComment! Got: %v", com)
		return jira.Comment{}, fmt.Errorf("Create JIRA ghComment failed: expected *jira.Comment; got %T", com)
	}
	return *co, nil
}

// UpdateComment updates a comment (identified by the `id` parameter) on a given
// JIRA with a new body from the fields of the given GitHub comment. It returns
// the updated comment.
func UpdateComment(j Client, issue jira.Issue, id string, comment github.IssueComment, g issuesyncgithub.Client) (jira.Comment, error) {
	config := j.getConfig()
	log := config.GetLogger()

	user, err := issuesyncgithub.GetUser(g, log, config.GetTimeout(), comment.User.GetLogin())
	if err != nil {
		return jira.Comment{}, err
	}

	body := fmt.Sprintf("Comment [(ID %d)|%s]", comment.GetID(), comment.GetHTMLURL())
	body = fmt.Sprintf("%s from GitHub user [%s|%s]", body, user.GetLogin(), user.GetHTMLURL())
	if user.GetName() != "" {
		body = fmt.Sprintf("%s (%s)", body, user.GetName())
	}
	body = fmt.Sprintf(
		"%s at %s:\n\n%s",
		body,
		comment.CreatedAt.Format(commentDateFormat),
		comment.GetBody(),
	)

	if len(body) > maxBodyLength {
		body = body[:maxBodyLength]
	}

	// As it is, the JIRA API we're using doesn't have any way to update comments natively.
	// So, we have to build the request ourselves.
	requestBody := struct {
		Body string `json:"body"`
	}{
		Body: body,
	}

	jComment := new(jira.Comment)

	if err != nil {
		log.Errorf("Error creating comment update request: %s", err)
		return jira.Comment{}, err
	}

	com, res, err := request(j.getConfig(), func() (interface{}, *jira.Response, error) {
		url := fmt.Sprintf("rest/api/2/issue/%s/comment/%s", issue.Key, id)
		res, err := j.do("PUT", url, requestBody, nil)
		return jComment, res, err
	})
	if err != nil {
		log.Errorf("Error updating comment: %v", err)
		return jira.Comment{}, getErrorBody(j.getConfig(), res)
	}
	co, ok := com.(*jira.Comment)
	if !ok {
		log.Errorf("Update JIRA comment did not return comment! Got: %v", com)
		return jira.Comment{}, fmt.Errorf("Update JIRA comment failed: expected *jira.Comment; got %T", com)
	}
	return *co, nil
}

// newlineReplaceRegex is a regex to match both "\r\n" and just "\n" newline styles,
// in order to allow us to escape both sequences cleanly in the output of a dry run.
var newlineReplaceRegex = regexp.MustCompile("\r?\n")

// truncate is a utility function to replace all the newlines in
// the string with the characters "\n", then truncate it to no
// more than 50 characters
func truncate(s string, length int) string {
	if s == "" {
		return "empty"
	}

	s = newlineReplaceRegex.ReplaceAllString(s, "\\n")
	if len(s) <= length {
		return s
	}
	return fmt.Sprintf("%s...", s[0:length])
}


