package cli

import (
	"context"
	"errors"
	"io/ioutil"

	"github.com/andygrunwald/go-jira"
	"github.com/cenkalti/backoff"
	"github.com/coreos/issue-sync/cfg"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

// GetErrorBody reads the HTTP response body of a JIRA API response,
// logs it as an error, and returns an error object with the contents
// of the body. If an error occurs during reading, that error is
// instead printed and returned. This function closes the body for
// further reading.
func GetErrorBody(config cfg.Config, res *jira.Response) error {
	log := config.GetLogger()
	defer res.Body.Close()
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Errorf("Error occured trying to read error body: %v", err)
		return err
	} else {
		log.Debugf("Error body: %s", body)
		return errors.New(string(body))
	}
}

// MakeGHRequest takes an API function from the GitHub library
// and calls it with exponential backoff. If the function succeeds, it
// stores the value in the ret parameter, and returns the HTTP response
// from the function, and a nil error. If it continues to fail until
// a maximum time is reached, the ret parameter is returned as is, and a
// nil HTTP response and a timeout error are returned.
//
// It is nearly identical to MakeJIRARequest, but returns a GitHub API response.
func MakeGHRequest(config cfg.Config, f func() (interface{}, *github.Response, error)) (interface{}, *github.Response, error) {
	var ret interface{}
	var res *github.Response
	var err error

	op := func() error {
		ret, res, err = f()
		return err
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = config.GetTimeout()

	er := backoff.Retry(op, b)
	if er != nil {
		return nil, nil, er
	}

	return ret, res, err
}

// MakeJIRARequest takes an API function from the JIRA library
// and calls it with exponential backoff. If the function succeeds, it
// stores the value in the ret parameter, and returns the HTTP response
// from the function, and a nil error. If it continues to fail until
// a maximum time is reached, the ret parameter is returned as is, and a
// nil HTTP response and a timeout error are returned.
//
// It is nearly identical to MakeGHRequest, but returns a JIRA API response.
func MakeJIRARequest(config cfg.Config, f func() (interface{}, *jira.Response, error)) (interface{}, *jira.Response, error) {
	var ret interface{}
	var res *jira.Response
	var err error

	op := func() error {
		ret, res, err = f()
		return err
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = config.GetTimeout()

	er := backoff.Retry(op, b)
	if er != nil {
		return ret, res, er
	}

	return ret, res, err
}

// GetGitHubClient initializes a GitHub API cli with an OAuth cli for authentication,
// then makes an API request to confirm that the service is running and the auth token
// is valid.
func GetGitHubClient(config cfg.Config) (*github.Client, error) {
	log := config.GetLogger()

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: config.GetConfigString("github-token")},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	// Make a request so we can check that we can connect fine.
	_, res, err := MakeGHRequest(config, func() (interface{}, *github.Response, error) {
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

// GetJIRAClient initializes a JIRA API cli, then sets the Basic Auth credentials
// passed to it. (OAuth token support is planned.)
//
// The validity of the cli and its authentication are not checked here. One way
// to check them would be to call cfg.LoadJIRAConfig() after this function.
func GetJIRAClient(config cfg.Config) (*jira.Client, error) {
	log := config.GetLogger()

	client, err := jira.NewClient(nil, config.GetConfigString("jira-uri"))
	if err != nil {
		log.Errorf("Error initializing JIRA cli; check your base URI. Error: %v", err)
		return nil, err
	}

	client.Authentication.SetBasicAuth(config.GetConfigString("jira-user"), config.GetConfigString("jira-pass"))

	log.Debug("JIRA cli initialized")
	return client, nil
}
