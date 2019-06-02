package models

import (
	"github.com/google/go-github/github"
)

type ExtendedGithubIssue struct {
	github.Issue
	ProjectCard *github.ProjectCard
	CommitIds []string
}