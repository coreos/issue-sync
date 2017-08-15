# issue-sync

Issue-sync is a tool for synchronizing GitHub and JIRA issues. It grew
out of a desire to maintain a public GitHub repo while tracking private
issues in a JIRA board; rather than require people to keep up with both
sources, we decided to make *one* the single source of truth.

## Usage

### JIRA Configuration

To use, first ensure you have a JIRA server with the project you want
to track on it - it can be a cloud account, or self-hosted. Also make
sure you have a user account that can access the project and create
issues on it; it's recommended that you create an account specifically
for the issue-sync tool.

Add the following custom fields to the project: `GitHub ID`, `GitHub
Number`, `GitHub Labels`, `GitHub Status`, `GitHub Reporter`, and `Last
Issue-Sync Update`. These fields are required and the names must match
exactly. In addition,  `GitHub ID` and `GitHub Number` must be number
fields, `Last Issue-Sync Update` must be a date time field, and the
remainder must be text fields.

If you intend to use OAuth with JIRA, you must create an inbound
application connection and add a public key. Instructions can be found
in
[OAuth for Rest APIs](https://developer.atlassian.com/cloud/jira/platform/jira-rest-api-oauth-authentication/).

### Application configuration

Arguments to the program may be passed on the command line or in a
JSON configuration file. For the command line arguments, run `issue-sync
help`. The JSON format is a single, flat object, with the argument long
names as keys.

Configuration arguments are as follows:

Name|Value Type|Example Value| Required|Default
----|----------|-------------|---------|-------------
log-level|string|"warn"|false|"info"
github-token|string| |true|null
jira-user|string|"user@jira.example.com"|false|null
jira-pass|string| |false|null
jira-token|string| |false|null
jira-secret|string| |false|null
jira-consumer-key|string| |false|null
jira-private-key-path|string| |false|null
repo-name|string|"coreos/issue-sync"|true|null
jira-uri|string|"https://jira.example.com|true|null
jira-project|string|"SYNC"|true|null
since|string|"2017-07-01T13:45:00-0800"|false|"1970-01-01T00:00:00+0000"
timeout|duration|500ms|false|1m

### Configuration Key Descriptions

`log-level` is the minimum level which will be logged; any output below
this value will be discarded.

`github-token` is a personal access token used to access GitHub as a
specific user.

`jira-user` and `jira-pass` are the username (i.e. email) and password
of the JIRA user which will be authenticated. See `Authentication` for
more details.

`jira-token` and `jira-secret` are OAuth access tokens which will be
used to perform an OAuth connection to JIRA. `jira-consumer-key` and
`jira-private-key-path` are the RSA key used for OAuth. See
`Authentication` for more details.

`repo-name` is the GitHub repo from which issues will be retrieved. It
must be in the form `owner/repo`, for example `coreos/issue-sync`.

`jira-uri` is the base URL of the JIRA instance. If the JIRA instance
lives at a non-root URL, the path must be included. For example,
`https://example.com/jira`.

`jira-project` is the key (not the name) of the project in JIRA to
which the issues will be synchronized.

`since` is the earliest the application will look for GitHub issue
updates. If an issue was last updated before this time, it will not
be synchronized. Usually this is the last run of the tool. It is in
ISO-8601 format.

`timeout` represents the duration of time for which an API request will
be retried in case of failure. Human-friendly strings such as `30s` are
accepted as input, although the application will save it to the file
in a number of nanoseconds.

### Configuration File

By default, issue-sync looks for the configuration file at
`$HOME/.issue-sync.json`. To override this location, use the `--config`
option on the command line.

If both a configuration file and command line arguments are provided,
the command line arguments override the configuration file.

After a successful run, the current configuration, with command line
arguments overwritten, is saved to the configuration file (either the
one provided, or `$HOME/.issue-sync.json`); the "since" date is updated
to the current date when the tool is run, as well.

### Authentication

If `jira-user` or `jira-pass` are provided, both are required, and the
application will connect to JIRA via Basic Authentication.

Otherwise, OAuth will be used. In this case, the `jira-consumer-key`, which is the
name of the RSA public key on the JIRA server, and the
`jira-private-key`, which is the path to the RSA private key which
matches, must be provided.

If the `jira-token` and `jira-secret` are provided, they are used as the
OAuth access token.

If they are not provided, an OAuth handshake occurs, and an authorization
URL will be given. The user will need to open the URL in their browser,
and receive the authorization code provided. Once the code is entered
into the application, an access token will be generated, and it will be
added to the configuration for future use.
