# issue-sync

Issue-sync is a tool for synchronizing GitHub and JIRA issues. It grew
out of a desire to maintain a public GitHub repo while tracking private
issues in a JIRA board; rather than require people to keep up with both
sources, we decided to make *one* the single source of truth.

## Usage

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

Arguments to the program may be passed on the command line or in a
JSON configuration file. For the command line arguments, run `issue-sync
help`. The JSON format is a single, flat object, with the argument long
names as keys.

Configuration arguments are as follows:

Name|Value Type|Example Value| Required|Default
----|----------|-------------|---------|-------------
log-level|string|"warn"|false|"info"
github-token|string| |true|null
jira-user|string|"user@jira.example.com"|true|null
jira-pass|string|"theUserPassword123"|true|null
repo-name|string|"coreos/issue-sync"|true|null
jira-uri|string|"https://jira.example.com|true|null
jira-project|string|"SYNC"|true|null
since|string|"2017-07-01T13:45:00-0800"|false|"1970-01-01T00:00:00+0000"

Note that the `repo-name` must include the owner and repo, that
`jira-uri` is the base URI of the JIRA instance, that `jira-project` is
the project's Key, not its name, and that `since` is in ISO-8601 format.

If the JIRA instance lives on a subdirectory of the server, that must
be included in the jira-uri. For example, at "https://example.com/jira".

"since" is the date including and after which the tool will look for
issue updates.

By default, issue-sync looks for the configuration file at
`$HOME/.issue-sync.json`. To override this location, use the `--config`
option on the command line.

If both a configuration file and command line arguments are provided,
the command line arguments override the configuration file.

After a successful run, the current configuration, with command line
arguments overwritten, is saved to the configuration file (either the
one provided, or `$HOME/.issue-sync.json`); the "since" date is updated
to the current date when the tool is run, as well.