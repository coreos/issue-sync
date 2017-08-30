package cfg

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestCreateTestConfig(t *testing.T) {
	v := viper.New()
	v.Set("abc", 123)
	v.Set("foo", "bar")

	config := CreateTestConfig(*v)

	if config.GetConfigString("foo") != "bar" {
		t.Errorf("Config value `foo` wrong: expected \"bar\"; got %q", config.GetConfigString("foo"))
	}
	if config.GetConfigString("abc") != "123" {
		t.Errorf("Config value `abc` wrong: expected \"123\"; got %q", config.GetConfigString("abc"))
	}
}

func CreateSampleCommand() cobra.Command {
	cm := cobra.Command{}

	cm.Flags().String("github-token", "foobar", "")
	cm.Flags().String("jira-user", "user", "")
	cm.Flags().String("jira-pass", "password", "")
	cm.Flags().String("repo-name", "coreos/issue-sync", "")
	cm.Flags().String("jira-uri", "https://example.com/", "")
	cm.Flags().String("jira-project", "TEST", "")
	cm.Flags().String("since", "2017-01-01T00:00:00+0000", "")
	cm.Flags().Duration("timeout", 30*time.Second, "")

	return cm
}

func TestNewConfig(t *testing.T) {
	cm := CreateSampleCommand()

	cfg, err := NewConfig(&cm)
	if err != nil {
		t.Fatalf("Error returned from config create: %v", err)
	}

	if cfg.GetConfigString("github-token") != "foobar" {
		t.Errorf("Wrong GitHub token: expected \"foobar\"; got %q", cfg.GetConfigString("github-token"))
	}
	if cfg.GetConfigString("jira-user") != "user" {
		t.Errorf("Wrong JIRA user: expected \"user\"; got %q", cfg.GetConfigString("jira-user"))
	}
	if cfg.GetConfigString("jira-pass") != "password" {
		t.Errorf("Wrong JIRA password: expected \"pass\"; got %q", cfg.GetConfigString("jira-pass"))
	}
	if cfg.GetConfigString("repo-name") != "coreos/issue-sync" {
		t.Errorf("Wrong GitHub repo: expected \"coreos/issue-sync\"; got %q", cfg.GetConfigString("repo-name"))
	}
	if cfg.GetConfigString("jira-uri") != "https://example.com/" {
		t.Errorf("Wrong JIRA URI: expected \"https://example.com/\"; got %q", cfg.GetConfigString("jira-uri"))
	}
	if cfg.GetConfigString("jira-project") != "TEST" {
		t.Errorf("Wrong JIRA user: expected \"TEST\"; got %q", cfg.GetConfigString("jira-project"))
	}
	expectedSince := time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC)
	if cfg.GetSinceParam().Unix() != expectedSince.Unix() {
		t.Errorf("Wrong since param: expected %v; got %v", expectedSince, cfg.GetSinceParam())
	}
	if cfg.GetTimeout() != 30*time.Second {
		t.Errorf("Wrong timeout: expected 30s; got %v", cfg.GetTimeout())
	}

	if cfg.GetConfigFile() != "" {
		t.Errorf("Config file not empty: %v", cfg.GetConfigFile())
	}
	if cfg.IsDryRun() {
		t.Errorf("Dry run is true; should be false!")
	}
	u,r := cfg.GetRepo()
	if u != "coreos" || r != "issue-sync" {
		t.Errorf("Repo is wrong! User: expected \"coreos\"; got %s. Repo: expected \"issue-sync\"; got %s", u, r)
	}
}

func TestIsBasicAuth_True(t *testing.T) {
	cm := CreateSampleCommand()

	config, err := NewConfig(&cm)
	if err != nil {
		t.Fatalf("Error creating config: %v", err)
	}

	if !config.IsBasicAuth() {
		t.Errorf("Basic auth is false; should be true!")
	}
}

func TestIsBasicAuth_False(t *testing.T) {
	cm := CreateSampleCommand()
	cm.Flags().Set("jira-user", "")
	cm.Flags().Set("jira-pass", "")

	cm.Flags().String("jira-token", "token", "")
	cm.Flags().String("jira-secret", "secret", "")
	// validateConfig() just checks that the file can be read, not that it's a private key
	cm.Flags().String("jira-private-key", "./config_test.go", "")
	cm.Flags().String("jira-consumer-key", "Private Key Name", "")

	config, err := NewConfig(&cm)
	if err != nil {
		t.Fatalf("Error creating config: %v", err)
	}

	if config.IsBasicAuth() {
		t.Errorf("Basic auth is true; should be false!")
	}
}
