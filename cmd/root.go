package cmd

import (
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/coreos/issue-sync/cfg"
	"github.com/coreos/issue-sync/cli"
	"github.com/coreos/issue-sync/lib"
	"github.com/spf13/cobra"
)

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
		config, err := cfg.NewConfig(cmd)
		if err != nil {
			return err
		}

		ghClient, err := cli.GetGitHubClient(config)
		if err != nil {
			return err
		}
		jiraClient, err := cli.GetJIRAClient(config)
		if err != nil {
			return err
		}

		if err := config.LoadJIRAConfig(*jiraClient); err != nil {
			return err
		}

		if err := lib.CompareIssues(config, *ghClient, *jiraClient); err != nil {
			return err
		}

		if !config.IsDryRun() {
			return config.SaveConfig()
		}

		return nil
	},
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
