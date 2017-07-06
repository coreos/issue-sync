package cmd

import (
	"strings"

	"os"

	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	log             *logrus.Logger
	defaultLogLevel = logrus.InfoLevel
	rootCmdFile     string
	rootCmdCfg      *viper.Viper
)

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

var RootCmd = &cobra.Command{
	Use:   "issue-sync [options]",
	Short: "A tool to synchronize GitHub and JIRA issues",
	Long:  "Full docs coming later; see https://github.com/coreos/issue-sync",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		rootCmdCfg.BindPFlags(cmd.Flags())
		initRootLogger()
	},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Called root job; not written yet")
	},
}

func init() {
	log = logrus.New()
	cobra.OnInitialize(func() {
		rootCmdCfg = newViper("issue-sync", rootCmdFile)
	})
	RootCmd.PersistentFlags().String("log-level", logrus.InfoLevel.String(), "Set the global log level")
	RootCmd.PersistentFlags().StringVar(&rootCmdFile, "config", "", "Config file (default is $HOME/.issue-sync.yaml)")
	RootCmd.PersistentFlags().String("github-token", "", "Set the API Token used to access the GitHub repo")
}

func initRootLogger() {
	log = logrus.New()

	logLevel := parseLogLevel(rootCmdCfg.GetString("log-level"))
	log.Level = logLevel
	log.WithField("log-level", logLevel).Debug("root log level set")
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
