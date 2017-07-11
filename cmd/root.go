package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/andygrunwald/go-jira"
	"github.com/fsnotify/fsnotify"
	"github.com/google/go-github/github"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

var (
	log             *logrus.Entry
	defaultLogLevel = logrus.InfoLevel
	rootCmdFile     string
	rootCmdCfg      *viper.Viper

	since               time.Time // The earliest GitHub issue updates we want to retrieve
	ghIDFieldID         string    // The customfield ID of the GitHub ID field in JIRA
	ghNumFieldID        string    // The customfield ID of the GitHub Number field in JIRA
	ghLabelsFieldID     string    // The customfield ID of the GitHub Labels field in JIRA
	ghStatusFieldID     string    // The customfield ID of the GitHub Status field in JIRA
	ghReporterFieldID   string    // The customfield ID of the GitHub Reporter field in JIRA
	isLastUpdateFieldID string    // The customfield ID of the Last Issue-Sync Update field in JIRA

	project jira.Project
)

const dateFormat = "2006-01-02T15:04:05-0700"

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

func GetGitHubClient(token string) (*github.Client, error) {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	// Make a request so we can check that we can connect fine.
	_, res, err := client.RateLimits(ctx)
	if err != nil {
		log.Errorf("Error connecting to GitHub; check your token. Error: %v", err)
		return nil, err
	} else if err = github.CheckResponse(res.Response); err != nil {
		log.Errorf("Error connecting to GitHub; check your token. Error: %v", err)
		return nil, err
	}

	log.Debugln("Successfully connected to GitHub.")
	return client, nil
}

func GetJIRAClient(username, password, baseURL string) (*jira.Client, error) {
	client, err := jira.NewClient(nil, baseURL)
	if err != nil {
		log.Errorf("Error initializing JIRA client; check your base URI. Error: %v", err)
		return nil, err
	}
	client.Authentication.SetBasicAuth(username, password)

	log.Debug("JIRA client initialized; getting project")

	proj, resp, err := client.Project.Get(rootCmdCfg.GetString("jira-project"))
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
	project = *proj

	log.Debug("Successfully connected to JIRA.")
	return client, nil
}

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

		ghClient, err := GetGitHubClient(rootCmdCfg.GetString("github-token"))
		if err != nil {
			return err
		}
		jiraClient, err := GetJIRAClient(
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

		return setLastUpdateTime()
	},
}

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
		return errors.New("Jira password required")
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

func getFieldIDs(client jira.Client) error {
	log.Debug("Collecting field IDs.")
	req, err := client.NewRequest("GET", "/rest/api/2/field", nil)
	if err != nil {
		return err
	}
	fields := new([]JIRAField)

	_, err = client.Do(req, fields)
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
		return errors.New("Could not find ID of 'GitHub ID' custom field. Check that it is named correctly.")
	} else if ghNumFieldID == "" {
		return errors.New("Could not find ID of 'GitHub Number' custom field. Check that it is named correctly.")
	} else if ghLabelsFieldID == "" {
		return errors.New("Could not find ID of 'Github Labels' custom field. Check that it is named correctly.")
	} else if ghStatusFieldID == "" {
		return errors.New("Could not find ID of 'Github Status' custom field. Check that it is named correctly.")
	} else if ghReporterFieldID == "" {
		return errors.New("Could not find ID of 'Github Reporter' custom field. Check that it is named correctly.")
	} else if isLastUpdateFieldID == "" {
		return errors.New("Could not find ID of 'Last Issue-Sync Update' custom field. Check that it is named correctly.")
	}

	log.Debug("All fields have been checked.")

	return nil
}

func compareIssues(ghClient github.Client, jiraClient jira.Client) error {
	log.Debug("Collecting issues")
	ctx := context.Background()

	repo := strings.Split(rootCmdCfg.GetString("repo-name"), "/")

	ghIssues, _, err := ghClient.Issues.ListByRepo(ctx, repo[0], repo[1], &github.IssueListByRepoOptions{
		Since: since,
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	})
	if err != nil {
		return err
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

	jiraIssues, _, err := jiraClient.Issue.Search(jql, nil)
	if err != nil {
		return err
	}

	log.Debug("Collected all JIRA issues")

	for _, ghIssue := range ghIssues {
		found := false
		for _, jIssue := range jiraIssues {
			id, _ := jIssue.Fields.Unknowns.Int(fmt.Sprintf("customfield_%s", ghIDFieldID))
			if int64(*ghIssue.ID) == id {
				found = true
				if err := updateIssue(*ghIssue, jIssue); err != nil {
					log.Errorf("Error updating issue %s. Error: %v", jIssue.Key, err)
				}
				break
			}
		}
		if !found {
			if err := createIssue(*ghIssue, jiraClient); err != nil {
				log.Errorf("Error creating issue for #%d. Error: %v", *ghIssue.Number, err)
			}
		}
	}

	return nil
}

func updateIssue(ghIssue github.Issue, jIssue jira.Issue, client jira.Client) error {
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
	if err != nil || strings.Join(labels, ",") != field {
		anyDifferent = true
		fields.Unknowns[key] = strings.Join(labels, ",")
	}

	if !anyDifferent {
		log.Debugf("JIRA issue %s is already up to date!", jIssue.Key)
		return nil
	}

	key = fmt.Sprintf("customfield_%s", isLastUpdateFieldID)
	fields.Unknowns[key] = time.Now().Format("2006-01-02T15:04:05-0700")

	fields.Type = jIssue.Fields.Type
	fields.Summary = jIssue.Fields.Summary

	issue := &jira.Issue{
		Fields: &fields,
		Key:    jIssue.Key,
		ID:     jIssue.ID,
	}

	issue, res, err := client.Issue.Update(issue)

	if err != nil {
		log.Errorf("Error updating JIRA issue %s: %s", jIssue.Key, err)
		defer res.Body.Close()
		body, _ := ioutil.ReadAll(res.Body)
		log.Debugf("Error body: %s", body)
		return err
	}

	log.Debugf("Successfully updated JIRA issue %s!", jIssue.Key)

	return nil
}

func createIssue(issue github.Issue, client jira.Client) error {
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
	fields.Unknowns[key] = time.Now().Format("2006-01-02T15:04:05-0700")

	jIssue := &jira.Issue{
		Fields: &fields,
	}

	jIssue, res, err := client.Issue.Create(jIssue)
	if err != nil {
		log.Errorf("Error creating JIRA issue: %s", err)
		defer res.Body.Close()
		body, _ := ioutil.ReadAll(res.Body)
		log.Debugf("Error body: %s", body)
		return err
	}

	log.Debugf("Created JIRA issue #%s!", jIssue.ID)
	return nil
}

type Config struct {
	LogLevel    string `json:"log-level" mapstructure:"log-level"`
	GithubToken string `json:"github-token" mapstructure:"github-token"`
	JiraUser    string `json:"jira-user" mapstructure:"jira-user"`
	JiraPass    string `json:"jira-pass" mapstructure:"jira-pass"`
	RepoName    string `json:"repo-name" mapstructure:"repo-name"`
	JiraUri     string `json:"jira-uri" mapstructure:"jira-uri"`
	JiraProject string `json:"jira-project" mapstructure:"jira-project"`
	Since       string `json:"since" mapstructure:"since"`
}

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

}

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
			log.WithError(err).Warningf("Error reading config file: %s", cfgFile)
		}
	}

	if log.Level == logrus.DebugLevel {
		v.Debug()
	}

	return v
}

func newLogger(app, level string) *logrus.Entry {
	logger := logrus.New()
	logger.Level = parseLogLevel(level)
	logEntry := logrus.NewEntry(logger).WithFields(logrus.Fields{
		"app": app,
	})
	logEntry.WithField("log-level", logger.Level).Info("log level set")
	return logEntry
}
