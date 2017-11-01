package clients

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"io/ioutil"
	"os"

	"github.com/coreos/issue-sync/cfg"
	"github.com/dghubble/oauth1"
)

// newJIRAHTTPClient obtains an access token (either from configuration
// or from an OAuth handshake) and creates an HTTP client that uses the
// token, which can be used to configure a JIRA client.
func newJIRAHTTPClient(config cfg.Config) (*http.Client, error) {
	ctx := context.Background()

	oauthConfig, err := oauthConfig(config)
	if err != nil {
		return nil, err
	}

	tok, ok := jiraTokenFromConfig(config)
	if !ok {
		tok, err = jiraTokenFromWeb(oauthConfig)
		if err != nil {
			return nil, err
		}
		config.SetJIRAToken(tok)
	}

	return oauthConfig.Client(ctx, tok), nil
}

// oauthConfig parses a private key and consumer key from the
// configuration, and creates an OAuth configuration which can
// be used to begin a handshake.
func oauthConfig(config cfg.Config) (oauth1.Config, error) {
	pvtKeyPath := config.GetConfigString("jira-private-key-path")

	pvtKeyFile, err := os.Open(pvtKeyPath)
	if err != nil {
		return oauth1.Config{}, fmt.Errorf("unable to open private key file for reading: %v", err)
	}

	pvtKey, err := ioutil.ReadAll(pvtKeyFile)
	if err != nil {
		return oauth1.Config{}, fmt.Errorf("unable to read contents of private key file: %v", err)
	}

	keyDERBlock, _ := pem.Decode(pvtKey)
	if keyDERBlock == nil {
		return oauth1.Config{}, errors.New("unable to decode private key PEM block")
	}
	if keyDERBlock.Type != "PRIVATE KEY" && !strings.HasSuffix(keyDERBlock.Type, " PRIVATE KEY") {
		return oauth1.Config{}, fmt.Errorf("unexpected private key DER block type: %s", keyDERBlock.Type)
	}

	key, err := x509.ParsePKCS1PrivateKey(keyDERBlock.Bytes)
	if err != nil {
		return oauth1.Config{}, fmt.Errorf("unable to parse PKCS1 private key: %v", err)
	}

	uri := config.GetConfigString("jira-uri")

	return oauth1.Config{
		ConsumerKey: config.GetConfigString("jira-consumer-key"),
		CallbackURL: "oob",
		Endpoint: oauth1.Endpoint{
			RequestTokenURL: fmt.Sprintf("%splugins/servlet/oauth/request-token", uri),
			AuthorizeURL:    fmt.Sprintf("%splugins/servlet/oauth/authorize", uri),
			AccessTokenURL:  fmt.Sprintf("%splugins/servlet/oauth/access-token", uri),
		},
		Signer: &oauth1.RSASigner{
			PrivateKey: key,
		},
	}, nil
}

// jiraTokenFromConfig attempts to load an OAuth access token from the
// application configuration file. It returns the token (or null if not
// configured) and an "ok" bool to indicate whether the token is provided.
func jiraTokenFromConfig(config cfg.Config) (*oauth1.Token, bool) {
	token := config.GetConfigString("jira-token")
	if token == "" {
		return nil, false
	}

	secret := config.GetConfigString("jira-secret")
	if secret == "" {
		return nil, false
	}

	return &oauth1.Token{
		Token:       token,
		TokenSecret: secret,
	}, true
}

// jiraTokenFromWeb performs an OAuth handshake, obtaining a request and
// then an access token by authorizing with the JIRA REST API.
func jiraTokenFromWeb(config oauth1.Config) (*oauth1.Token, error) {
	requestToken, requestSecret, err := config.RequestToken()
	if err != nil {
		return nil, fmt.Errorf("unable to get request token: %v", err)
	}

	authURL, err := config.AuthorizationURL(requestToken)
	if err != nil {
		return nil, fmt.Errorf("unable to get authorize URL: %v", err)
	}

	fmt.Printf("Please go to the following URL in your browser:\n%v\n\n", authURL.String())
	fmt.Print("Authorization code: ")

	var code string
	_, err = fmt.Scan(&code)
	fmt.Println()
	if err != nil {
		return nil, fmt.Errorf("unable to read auth code: %v", err)
	}

	accessToken, accessSecret, err := config.AccessToken(requestToken, requestSecret, code)
	if err != nil {
		return nil, fmt.Errorf("unable to get access token: %v", err)
	}

	return oauth1.NewToken(accessToken, accessSecret), nil
}
