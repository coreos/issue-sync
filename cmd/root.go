package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/andygrunwald/go-jira"
	"github.com/cenkalti/backoff"
	"github.com/fsnotify/fsnotify"
	"github.com/google/go-github/github"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/oauth2"
)

var (
	// log is a globally accessibly logrus logger.
	log *logrus.Entry
	// defaultLogLevel is the level logrus should default to if the configured option can't be parsed.
	defaultLogLevel = logrus.InfoLevel
	// rootCmdFile is the file viper loads the configuration from (default $HOME/.issue-sync.json).
	rootCmdFile string
	// rootCmdCfg is the configuration object; it merges command line options, config files, and defaults.
	rootCmdCfg *viper.Viper

	// since is the time we use to filter issues. Only GitHub issues updated after the `since` date
	// will be requsted.
	since time.Time
	// ghIDFieldID is the customfield ID of the GitHub ID field in JIRA.
	ghIDFieldID string
	// ghNumFieldID is the customfield ID of the GitHub Number field in JIRA.
	ghNumFieldID string
	// ghlabelsFieldID is the customfield ID of the GitHub Labels field in JIRA.
	ghLabelsFieldID string
	// ghStatusFieldID is the customfield ID of the GitHub Status field in JIRA.
	ghStatusFieldID string
	// ghReporterFieldID is the customfield ID of the GitHub Reporter field in JIRA.
	ghReporterFieldID string
	// isLastUpdateFieldID is the customfield ID of the Last Issue-Sync Update field in JIRA.
	isLastUpdateFieldID string

	// project is the JIRA project set on the command line; it is a JIRA API object
	// from which we can retrieve any data.
	project jira.Project

	// dryRun configures whether the application calls the create/update endpoints of the JIRA
	// API or just prints out the actions it would take.
	dryRun bool
)

// dateFormat is the format used for the `Last Issue-Sync Update` field.
const dateFormat = "2006-01-02T15:04:05-0700"

// commentDateFormat is the format used in the headers of JIRA comments.
const commentDateFormat = "15:04 PM, January 2 2006"

// Execute provides a single function to run the root command and handle errors.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

// getErrorBody reads the HTTP response body of a JIRA API response,
// logs it as an error, and returns an error object with the contents
// of the body. If an error occurs during reading, that error is
// instead printed and returned. This function closes the body for
// further reading.
func getErrorBody(res *jira.Response) error {
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Errorf("Error occured trying to read error body: %v", err)
		return err
	}

	log.Debugf("Error body: %s", body)
	return errors.New(string(body))
}

// makeGHRequest takes an API function from the GitHub library
// and calls it with exponential backoff. If the function succeeds, it
// stores the value in the ret parameter, and returns the HTTP response
// from the function, and a nil error. If it continues to fail until
// a maximum time is reached, the ret parameter is returned as is, and a
// nil HTTP response and a timeout error are returned.
//
// It is nearly identical to makeJIRARequest, but returns a GitHub API response.
func makeGHRequest(f func() (interface{}, *github.Response, error)) (interface{}, *github.Response, error) {
	var ret interface{}
	var res *github.Response
	var err error

	op := func() error {
		ret, res, err = f()
		return err
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = rootCmdCfg.GetDuration("timeout")

	er := backoff.Retry(op, b)
	if er != nil {
		return nil, nil, er
	}

	return ret, res, err
}

// makeJIRARequest takes an API function from the JIRA library
// and calls it with exponential backoff. If the function succeeds, it
// stores the value in the ret parameter, and returns the HTTP response
// from the function, and a nil error. If it continues to fail until
// a maximum time is reached, the ret parameter is returned as is, and a
// nil HTTP response and a timeout error are returned.
//
// It is nearly identical to makeGHRequest, but returns a JIRA API response.
func makeJIRARequest(f func() (interface{}, *jira.Response, error)) (interface{}, *jira.Response, error) {
	var ret interface{}
	var res *jira.Response
	var err error

	op := func() error {
		ret, res, err = f()
		return err
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = rootCmdCfg.GetDuration("timeout")

	er := backoff.Retry(op, b)
	if er != nil {
		return nil, nil, er
	}

	return ret, res, err
}

// getGitHubClient initializes a GitHub API client with an OAuth client for authentication,
// then makes an API request to confirm that the service is running and the auth token
// is valid.
func getGitHubClient(token string) (*github.Client, error) {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	// Make a request so we can check that we can connect fine.
	_, res, err := makeGHRequest(func() (interface{}, *github.Response, error) {
		return client.RateLimits(ctx)
	})
	if err != nil {
		log.Errorf("Error connecting to GitHub; check your token. Error: %v", err)
		return nil, err
	} else if err = github.CheckResponse(res.Response); err != nil {
		log.Errorf("Error connecting to GitHub; check your token. Error: %v", err)
		return nil, err
	}

	log.Debug("Successfully connected to GitHub.")
	return client, nil
}

// getJIRAClient initializes a JIRA API client, then sets the Basic Auth credentials
// passed to it. (OAuth token support is planned.) It then requests the project using
// the key provided on the command line to have it accessible by future functions and
// to check that the API is accessible and the auth credentials are valid.
func getJIRAClient(username, password, baseURL string) (*jira.Client, error) {
	client, err := jira.NewClient(nil, baseURL)
	if err != nil {
		log.Errorf("Error initializing JIRA client; check your base URI. Error: %v", err)
		return nil, err
	}
	client.Authentication.SetBasicAuth(username, password)

	log.Debug("JIRA client initialized; getting project")

	proj, resp, err := makeJIRARequest(func() (interface{}, *jira.Response, error) {
		return client.Project.Get(rootCmdCfg.GetString("jira-project"))
	})
	if err != nil {
		log.Errorf("Unknown error using JIRA client. Error: %v", err)
		return nil, err
	} else if resp.StatusCode == 404 {
		log.Errorf("Error retrieving JIRA project; check your key. Error: %v", err)
		return nil, jira.CheckResponse(resp.Response)
	} else if resp.StatusCode == 401 {
		log.Errorf("Error connecting to JIRA; check your credentials. Error: %v", err)
		return nil, jira.CheckResponse(resp.Response)
	}

	p, ok := proj.(*jira.Project)
	if !ok {
		log.Errorf("Get JIRA project did not return project! Value: %v", proj)
		return nil, fmt.Errorf("Get project failed: expected *jira.Project; got %T", proj)
	}
	project = *p

	log.Debug("Successfully connected to JIRA.")
	return client, nil
}

// RootCmd represents the command itself and configures it.
var RootCmd = &cobra.Command{
	Use:   "issue-sync [options]",
	Short: "A tool to synchronize GitHub and JIRA issues",
	Long:  "Full docs coming later; see https://github.com/coreos/issue-sync",
	PreRun: func(cmd *cobra.Command, args []string) {
		rootCmdCfg.BindPFlags(cmd.Flags())
		log = newLogger("issue-sync", rootCmdCfg.GetString("log-level"))
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateConfig(); err != nil {
			return err
		}

		ghClient, err := getGitHubClient(rootCmdCfg.GetString("github-token"))
		if err != nil {
			return err
		}
		jiraClient, err := getJIRAClient(
			rootCmdCfg.GetString("jira-user"),
			rootCmdCfg.GetString("jira-pass"),
			rootCmdCfg.GetString("jira-uri"),
		)
		if err != nil {
			return err
		}

		if err := getFieldIDs(*jiraClient); err != nil {
			return err
		}

		if err := compareIssues(*ghClient, *jiraClient); err != nil {
			return err
		}

		if !dryRun {
			return setLastUpdateTime()
		}

		return nil
	},
}

// validateConfig checks the values provided to all of the configuration
// options, ensuring that e.g. `since` is a valid date, `jira-uri` is a
// real URI, etc. This is the first level of checking. It does not confirm
// if a JIRA server is running at `jira-uri` for example; that is checked
// in getJIRAClient when we actually make a call to the API.
func validateConfig() error {
	// Log level and config file location are validated already

	log.Debug("Checking config variables...")
	token := rootCmdCfg.GetString("github-token")
	if token == "" {
		return errors.New("GitHub token required")
	}

	jUser := rootCmdCfg.GetString("jira-user")
	if jUser == "" {
		return errors.New("Jira username required")
	}

	jPass := rootCmdCfg.GetString("jira-pass")
	if jPass == "" {
		fmt.Print("Enter your JIRA password: ")
		bytePass, err := terminal.ReadPassword(int(syscall.Stdin))
		if err != nil {
			return errors.New("Jira password required")
		}
		rootCmdCfg.Set("jira-pass", string(bytePass))
	}

	repo := rootCmdCfg.GetString("repo-name")
	if repo == "" {
		return errors.New("GitHub repository required")
	}
	if !strings.Contains(repo, "/") || len(strings.Split(repo, "/")) != 2 {
		return errors.New("GitHub repository must be of form user/repo")
	}

	uri := rootCmdCfg.GetString("jira-uri")
	if uri == "" {
		return errors.New("JIRA URI required")
	}
	if _, err := url.ParseRequestURI(uri); err != nil {
		return errors.New("JIRA URI must be valid URI")
	}

	project := rootCmdCfg.GetString("jira-project")
	if project == "" {
		return errors.New("JIRA project required")
	}

	sinceStr := rootCmdCfg.GetString("since")
	if sinceStr == "" {
		rootCmdCfg.Set("since", "1970-01-01T00:00:00+0000")
	}
	var err error
	since, err = time.Parse(dateFormat, sinceStr)
	if err != nil {
		return errors.New("Since date must be in ISO-8601 format")
	}
	log.Debug("All config variables are valid!")

	return nil
}

// JIRAField represents field metadata in JIRA. For an example of its
// structure, make a request to `${jira-uri}/rest/api/2/field`.
type JIRAField struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Custom      bool     `json:"custom"`
	Orderable   bool     `json:"orderable"`
	Navigable   bool     `json:"navigable"`
	Searchable  bool     `json:"searchable"`
	ClauseNames []string `json:"clauseNames"`
	Schema      struct {
		Type     string `json:"type"`
		System   string `json:"system,omitempty"`
		Items    string `json:"items,omitempty"`
		Custom   string `json:"custom,omitempty"`
		CustomID int    `json:"customId,omitempty"`
	} `json:"schema,omitempty"`
}

// getFieldIDs requests the metadata of every issue field in the JIRA
// project and saves the IDs of the custom fields used by issue-sync.
func getFieldIDs(client jira.Client) error {
	log.Debug("Collecting field IDs.")
	req, err := client.NewRequest("GET", "/rest/api/2/field", nil)
	if err != nil {
		return err
	}
	fields := new([]JIRAField)

	_, _, err = makeJIRARequest(func() (interface{}, *jira.Response, error) {
		res, err := client.Do(req, fields)
		return nil, res, err
	})
	if err != nil {
		return err
	}

	for _, field := range *fields {
		switch field.Name {
		case "GitHub ID":
			ghIDFieldID = fmt.Sprint(field.Schema.CustomID)
		case "GitHub Number":
			ghNumFieldID = fmt.Sprint(field.Schema.CustomID)
		case "GitHub Labels":
			ghLabelsFieldID = fmt.Sprint(field.Schema.CustomID)
		case "GitHub Status":
			ghStatusFieldID = fmt.Sprint(field.Schema.CustomID)
		case "GitHub Reporter":
			ghReporterFieldID = fmt.Sprint(field.Schema.CustomID)
		case "Last Issue-Sync Update":
			isLastUpdateFieldID = fmt.Sprint(field.Schema.CustomID)
		}
	}

	if ghIDFieldID == "" {
		return errors.New("could not find ID of 'GitHub ID' custom field. Check that it is named correctly.")
	} else if ghNumFieldID == "" {
		return errors.New("could not find ID of 'GitHub Number' custom field. Check that it is named correctly.")
	} else if ghLabelsFieldID == "" {
		return errors.New("could not find ID of 'Github Labels' custom field. Check that it is named correctly.")
	} else if ghStatusFieldID == "" {
		return errors.New("could not find ID of 'Github Status' custom field. Check that it is named correctly.")
	} else if ghReporterFieldID == "" {
		return errors.New("could not find ID of 'Github Reporter' custom field. Check that it is named correctly.")
	} else if isLastUpdateFieldID == "" {
		return errors.New("could not find ID of 'Last Issue-Sync Update' custom field. Check that it is named correctly.")
	}

	log.Debug("All fields have been checked.")

	return nil
}

// compareIssues gets the list of GitHub issues updated since the `since` date,
// gets the list of JIRA issues which have GitHub ID custom fields in that list,
// then matches each one. If a JIRA issue already exists for a given GitHub issue,
// it updates the issue; if no JIRA issue already exists, it creates one.
func compareIssues(ghClient github.Client, jiraClient jira.Client) error {
	log.Debug("Collecting issues")
	ctx := context.Background()

	repo := strings.Split(rootCmdCfg.GetString("repo-name"), "/")

	i, _, err := makeGHRequest(func() (interface{}, *github.Response, error) {
		return ghClient.Issues.ListByRepo(ctx, repo[0], repo[1], &github.IssueListByRepoOptions{
			Since: since,
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
		rootCmdCfg.GetString("jira-project"), ghIDFieldID, strings.Join(ids, ","))

	ji, res, err := makeJIRARequest(func() (interface{}, *jira.Response, error) {
		return jiraClient.Issue.Search(jql, nil)
	})
	if err != nil {
		log.Errorf("Error retrieving JIRA issues: %v", err)
		return getErrorBody(res)
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
			id, _ := jIssue.Fields.Unknowns.Int(fmt.Sprintf("customfield_%s", ghIDFieldID))
			if int64(*ghIssue.ID) == id {
				found = true
				if err := updateIssue(*ghIssue, jIssue, ghClient, jiraClient); err != nil {
					log.Errorf("Error updating issue %s. Error: %v", jIssue.Key, err)
				}
				break
			}
		}
		if !found {
			if err := createIssue(*ghIssue, ghClient, jiraClient); err != nil {
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
func updateIssue(ghIssue github.Issue, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
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

	key := fmt.Sprintf("customfield_%s", ghStatusFieldID)
	field, err := jIssue.Fields.Unknowns.String(key)
	if err != nil || *ghIssue.State != field {
		anyDifferent = true
		fields.Unknowns[key] = *ghIssue.State
	}

	key = fmt.Sprintf("customfield_%s", ghReporterFieldID)
	field, err = jIssue.Fields.Unknowns.String(key)
	if err != nil || *ghIssue.User.Login != field {
		anyDifferent = true
		fields.Unknowns[key] = *ghIssue.User.Login
	}

	labels := make([]string, len(ghIssue.Labels))
	for i, l := range ghIssue.Labels {
		labels[i] = *l.Name
	}

	key = fmt.Sprintf("customfield_%s", ghLabelsFieldID)
	field, err = jIssue.Fields.Unknowns.String(key)
	if err != nil && strings.Join(labels, ",") != field {
		anyDifferent = true
		fields.Unknowns[key] = strings.Join(labels, ",")
	}

	if anyDifferent {
		key = fmt.Sprintf("customfield_%s", isLastUpdateFieldID)
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

		if !dryRun {
			_, res, err := makeJIRARequest(func() (interface{}, *jira.Response, error) {
				return jClient.Issue.Update(issue)
			})

			if err != nil {
				log.Errorf("Error updating JIRA issue %s: %v", jIssue.Key, err)
				return getErrorBody(res)
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
			key := fmt.Sprintf("customfield_%s", ghLabelsFieldID)
			if labels, err := fields.Unknowns.String(key); err == nil {
				log.Infof("  Labels: %s", labels)
			}
			key = fmt.Sprintf("customfield_%s", ghStatusFieldID)
			if state, err := fields.Unknowns.String(key); err == nil {
				log.Infof("  State: %s", state)
			}
			log.Info("")
		}

		log.Debugf("Successfully updated JIRA issue %s!", jIssue.Key)
	} else {
		log.Debugf("JIRA issue %s is already up to date!", jIssue.Key)
	}

	i, _, err := makeJIRARequest(func() (interface{}, *jira.Response, error) {
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

	if err = createComments(ghIssue, jIssue, comments, ghClient, jClient); err != nil {
		return err
	}

	return nil
}

// createIssue generates a JIRA issue from the various fields on the given GitHub issue then
// sends it to the JIRA API.
func createIssue(issue github.Issue, ghClient github.Client, jClient jira.Client) error {
	log.Debugf("Creating JIRA issue based on GitHub issue #%d", *issue.Number)

	fields := jira.IssueFields{
		Type: jira.IssueType{
			Name: "Task", // TODO: Determine issue type
		},
		Project:     project,
		Summary:     *issue.Title,
		Description: *issue.Body,
		Unknowns:    map[string]interface{}{},
	}

	key := fmt.Sprintf("customfield_%s", ghIDFieldID)
	fields.Unknowns[key] = *issue.ID
	key = fmt.Sprintf("customfield_%s", ghNumFieldID)
	fields.Unknowns[key] = *issue.Number
	key = fmt.Sprintf("customfield_%s", ghStatusFieldID)
	fields.Unknowns[key] = *issue.State
	key = fmt.Sprintf("customfield_%s", ghReporterFieldID)
	fields.Unknowns[key] = issue.User.GetLogin()
	key = fmt.Sprintf("customfield_%s", ghLabelsFieldID)
	strs := make([]string, len(issue.Labels))
	for i, v := range issue.Labels {
		strs[i] = *v.Name
	}
	fields.Unknowns[key] = strings.Join(strs, ",")
	key = fmt.Sprintf("customfield_%s", isLastUpdateFieldID)
	fields.Unknowns[key] = time.Now().Format(dateFormat)

	jIssue := &jira.Issue{
		Fields: &fields,
	}

	if !dryRun {
		i, res, err := makeJIRARequest(func() (interface{}, *jira.Response, error) {
			return jClient.Issue.Create(jIssue)
		})
		if err != nil {
			log.Errorf("Error creating JIRA issue: %v", err)
			return getErrorBody(res)
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
		key := fmt.Sprintf("customfield_%s", ghLabelsFieldID)
		log.Infof("  Labels: %s", fields.Unknowns[key])
		key = fmt.Sprintf("customfield_%s", ghStatusFieldID)
		log.Infof("  State: %s", fields.Unknowns[key])
		key = fmt.Sprintf("customfield_%s", ghReporterFieldID)
		log.Infof("  Reporter: %s", fields.Unknowns[key])
		log.Info("")
	}

	log.Debugf("Created JIRA issue %s!", jIssue.Key)

	if err := createComments(issue, *jIssue, nil, ghClient, jClient); err != nil {
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
func createComments(ghIssue github.Issue, jIssue jira.Issue, existing []jira.Comment, ghClient github.Client, jClient jira.Client) error {
	if *ghIssue.Comments == 0 {
		log.Debugf("Issue #%d has no comments, skipping.", *ghIssue.Number)
		return nil
	}

	ctx := context.Background()
	repo := strings.Split(rootCmdCfg.GetString("repo-name"), "/")
	c, _, err := makeGHRequest(func() (interface{}, *github.Response, error) {
		return ghClient.Issues.ListComments(ctx, repo[0], repo[1], *ghIssue.Number, &github.IssueListCommentsOptions{
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

			updateComment(*ghComment, jComment, jIssue, ghClient, jClient)
			break
		}
		if found {
			continue
		}

		if err := createComment(*ghComment, jIssue, ghClient, jClient); err != nil {
			return err
		}
	}

	log.Debugf("Copied comments from GH issue #%d to JIRA issue %s.", *ghIssue.Number, jIssue.Key)
	return nil
}

// updateComment compares the body of a GitHub comment with the body (minus header)
// of the JIRA comment, and updates the JIRA comment if necessary.
func updateComment(ghComment github.IssueComment, jComment jira.Comment, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
	// fields[0] is the whole body, 1 is the ID, 2 is the username, 3 is the real name (or "" if none)
	// 4 is the date, and 5 is the real body
	fields := jCommentRegex.FindStringSubmatch(jComment.Body)

	if fields[5] == *ghComment.Body {
		return nil
	}

	u, _, err := makeGHRequest(func() (interface{}, *github.Response, error) {
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

	if !dryRun {
		req, err := jClient.NewRequest("PUT", fmt.Sprintf("rest/api/2/issue/%s/comment/%s", jIssue.Key, jComment.ID), request)
		if err != nil {
			log.Errorf("Error creating comment update request: %v", err)
			return err
		}

		_, res, err := makeJIRARequest(func() (interface{}, *jira.Response, error) {
			res, err := jClient.Do(req, nil)
			return nil, res, err
		})
		if err != nil {
			log.Errorf("Error updating comment: %v", err)
			return getErrorBody(res)
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
func createComment(ghComment github.IssueComment, jIssue jira.Issue, ghClient github.Client, jClient jira.Client) error {
	u, _, err := makeGHRequest(func() (interface{}, *github.Response, error) {
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

	if !dryRun {
		_, res, err := makeJIRARequest(func() (interface{}, *jira.Response, error) {
			return jClient.Issue.AddComment(jIssue.ID, jComment)
		})
		if err != nil {
			log.Errorf("Error creating JIRA comment on issue %s. Error: %v", jIssue.Key, err)
			return getErrorBody(res)
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

// Config represents the structure of the JSON configuration file used by Viper.
type Config struct {
	LogLevel    string        `json:"log-level" mapstructure:"log-level"`
	GithubToken string        `json:"github-token" mapstructure:"github-token"`
	JiraUser    string        `json:"jira-user" mapstructure:"jira-user"`
	RepoName    string        `json:"repo-name" mapstructure:"repo-name"`
	JiraURI     string        `json:"jira-uri" mapstructure:"jira-uri"`
	JiraProject string        `json:"jira-project" mapstructure:"jira-project"`
	Since       string        `json:"since" mapstructure:"since"`
	Timeout     time.Duration `json:"timeout" mapstructure:"timeout"`
}

// setLastUpdateTime sets the `since` date of the current configuration to the
// present date, then serializes the configuration into JSON and saves it to
// the currently used configuration file (default $HOME/.issue-sync.json)
func setLastUpdateTime() error {
	rootCmdCfg.Set("since", time.Now().Format(dateFormat))

	var c Config
	rootCmdCfg.Unmarshal(&c)

	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	f, err := os.OpenFile(rootCmdCfg.ConfigFileUsed(), os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	f.WriteString(string(b))

	return nil
}

func init() {
	log = logrus.NewEntry(logrus.New())
	cobra.OnInitialize(func() {
		rootCmdCfg = newViper("issue-sync", rootCmdFile)
	})
	RootCmd.PersistentFlags().String("log-level", logrus.InfoLevel.String(), "Set the global log level")
	RootCmd.PersistentFlags().StringVar(&rootCmdFile, "config", "", "Config file (default is $HOME/.issue-sync.yaml)")
	RootCmd.PersistentFlags().StringP("github-token", "t", "", "Set the API Token used to access the GitHub repo")
	RootCmd.PersistentFlags().StringP("jira-user", "u", "", "Set the JIRA username to authenticate with")
	RootCmd.PersistentFlags().StringP("jira-pass", "p", "", "Set the JIRA password to authenticate with")
	RootCmd.PersistentFlags().StringP("repo-name", "r", "", "Set the repository path (should be form owner/repo)")
	RootCmd.PersistentFlags().StringP("jira-uri", "U", "", "Set the base uri of the JIRA instance")
	RootCmd.PersistentFlags().StringP("jira-project", "P", "", "Set the key of the JIRA project")
	RootCmd.PersistentFlags().StringP("since", "s", "1970-01-01T00:00:00+0000", "Set the day that the update should run forward from")
	RootCmd.PersistentFlags().BoolVarP(&dryRun, "dry-run", "d", false, "Print out actions to be taken, but do not execute them")
	RootCmd.PersistentFlags().DurationP("timeout", "T", time.Minute, "Set the maximum timeout on all API calls")
}

// parseLogLevel is a helper function to parse the log level passed in the
// configuration into a logrus Level, or to use the default log level set
// above if the log level can't be parsed.
func parseLogLevel(level string) logrus.Level {
	if level == "" {
		return defaultLogLevel
	}

	ll, err := logrus.ParseLevel(level)
	if err != nil {
		fmt.Printf("Failed to parse log level, using default. Error: %v\n", err)
		return defaultLogLevel
	}
	return ll
}

// newViper generates a viper configuration object which
// merges (in order from highest to lowest priority) the
// command line options, configuration file options, and
// default configuration values. This viper object becomes
// the single source of truth for the app configuration.
func newViper(appName, cfgFile string) *viper.Viper {
	v := viper.New()

	v.SetEnvPrefix(appName)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	v.SetConfigName(fmt.Sprintf("config-%s", appName))
	v.AddConfigPath(".")
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	}

	if err := v.ReadInConfig(); err == nil {
		log.WithField("file", v.ConfigFileUsed()).Infof("config file loaded")
		v.WatchConfig()
		v.OnConfigChange(func(e fsnotify.Event) {
			log.WithField("file", e.Name).Info("config file changed")
		})
	} else {
		if cfgFile != "" {
			log.WithError(err).Warningf("Error reading config file: %v", cfgFile)
		}
	}

	if log.Level == logrus.DebugLevel {
		v.Debug()
	}

	return v
}

// newLogger uses the log level provided in the configuration
// to create a new logrus logger and set fields on it to make
// it easy to use.
func newLogger(app, level string) *logrus.Entry {
	logger := logrus.New()
	logger.Level = parseLogLevel(level)
	logEntry := logrus.NewEntry(logger).WithFields(logrus.Fields{
		"app": app,
	})
	logEntry.WithField("log-level", logger.Level).Info("log level set")
	return logEntry
}
