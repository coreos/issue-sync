package clients

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/coreos/issue-sync/cfg"
	"github.com/google/go-github/github"
	"github.com/spf13/viper"
)

func TestGitHubRequest_Pass(t *testing.T) {
	v := viper.New()
	v.Set("timeout", 10*time.Second)
	ghClient := realGHClient{
		config: cfg.CreateTestConfig(*v),
	}

	i := 0
	f := func() (interface{}, *github.Response, error) {
		i++
		return i, nil, nil
	}

	reps, res, err := ghClient.request(f)
	if err != nil {
		t.Fatalf("Error was not null! Err: %v", err)
	}
	if res != nil {
		t.Fatalf("Response was not null! Res: %v (%T)", res, res)
	}

	repsi, ok := reps.(int)
	if !ok {
		t.Fatalf("Wrong request return type: expected int; got %T", reps)
	}
	if repsi != 1 {
		t.Errorf("Wrong request return value: expected 1; got %d", repsi)
	}
}

func TestGitHubRequest_TakesTime(t *testing.T) {
	v := viper.New()
	v.Set("timeout", 30*time.Second)
	ghClient := realGHClient{
		config: cfg.CreateTestConfig(*v),
	}

	i := 0
	f := func() (interface{}, *github.Response, error) {
		i++
		if i < 5 {
			return i, nil, errors.New("Keep repeating!")
		}
		return i, nil, nil
	}

	reps, res, err := ghClient.request(f)
	if err != nil {
		t.Fatalf("Error was not null! Err: %v", err)
	}
	if res != nil {
		t.Fatalf("Response was not null! Res: %v (%T)", res, res)
	}

	repsi, ok := reps.(int)
	if !ok {
		t.Fatalf("Wrong request return type: expected int; got %T", reps)
	}
	if repsi != 5 {
		t.Errorf("Wrong request return value: expected 5; got %d", repsi)
	}
}

func TestGitHubRequest_Errors(t *testing.T) {
	v := viper.New()
	v.Set("timeout", 5*time.Second)
	ghClient := realGHClient{
		config: cfg.CreateTestConfig(*v),
	}

	i := 0
	f := func() (interface{}, *github.Response, error) {
		i++
		return i, nil, errors.New("Keep repeating!")
	}

	reps, res, err := ghClient.request(f)
	if err == nil {
		t.Fatalf("Error was null, but should have thrown!")
	}
	if res != nil {
		t.Fatalf("Response was not null! Res: %v (%T)", res, res)
	}
	fmt.Printf("%v", reps)
	if reps != nil {
		t.Errorf("Request result was not null! Result: %v (%T)", reps, reps)
	}
}
