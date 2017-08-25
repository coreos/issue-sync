package lib

import (
	"fmt"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/andygrunwald/go-jira"
	"github.com/coreos/issue-sync/cfg"
	"github.com/dghubble/oauth1"
)

/* This file holds functions and structures to be used by all of the lib/ tests,
 * including a test cfg.Config object.
 */

const ghIDFieldID = "10101"
const ghNumberFieldID = "10102"
const ghLabelsFieldID = "10103"
const ghStatusFieldID = "10104"
const ghReporterFieldID = "10105"
const isLastUpdateFieldID = "10106"

const ghIDFieldKey = "customfield_10101"
const ghNumberFieldKey = "customfield_10102"
const ghLabelsFieldKey = "customfield_10103"
const ghStatusFieldKey = "customfield_10104"
const ghReporterFieldKey = "customfield_10105"
const isLastUpdateFieldKey = "customfield_10106"

func NewTestConfig() cfg.Config {
	var config cfg.Config
	config = testConfig{
		log: *logrus.New().WithField("app-name", "issue-sync-test"),
	}

	return config
}

type testConfig struct {
	log logrus.Entry
}

func (c testConfig) LoadJIRAConfig(jira.Client) (cfg.Config, error) {
	return c, nil
}

func (testConfig) GetConfigFile() string {
	return ""
}

func (testConfig) GetConfigString(string) string {
	return ""
}

func (testConfig) IsBasicAuth() bool {
	return false
}

func (testConfig) GetSinceParam() time.Time {
	return time.Now()
}

func (c testConfig) GetLogger() logrus.Entry {
	return c.log
}

func (testConfig) IsDryRun() bool {
	return false
}

func (testConfig) GetTimeout() time.Duration {
	return time.Second
}

func (testConfig) GetFieldID(key cfg.FieldKey) string {
	switch key {
	case cfg.GitHubID:
		return ghIDFieldID
	case cfg.GitHubNumber:
		return ghNumberFieldID
	case cfg.GitHubLabels:
		return ghLabelsFieldID
	case cfg.GitHubStatus:
		return ghStatusFieldID
	case cfg.GitHubReporter:
		return ghReporterFieldID
	case cfg.LastISUpdate:
		return isLastUpdateFieldID
	default:
		return ""
	}
}

func (c testConfig) GetFieldKey(key cfg.FieldKey) string {
	return fmt.Sprintf("customfield_%s", c.GetFieldID(key))
}

func (testConfig) GetProject() jira.Project {
	return jira.Project{
		Key:  "TEST",
		Name: "Test",
	}
}

func (testConfig) GetProjectKey() string {
	return "TEST"
}

func (testConfig) GetRepo() (string, string) {
	return "coreos", "issue-sync"
}

func (testConfig) SetJIRAToken(*oauth1.Token) {
}

func (testConfig) SaveConfig() error {
	return nil
}

func (testConfig) validateConfig() error {
	return nil
}
