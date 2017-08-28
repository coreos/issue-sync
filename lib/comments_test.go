package lib

import "testing"

func TestCompareComments(t *testing.T) {
	setup()

	if err := setup(); err != nil {
		t.Fatalf("Error running setup: %v", err)
	}

	gh := testGHClient{}
	jc := &testJIRAClient{}

	config := NewTestConfig()

	ghIssue := ghIssues[0]
	jIssue := jiraIssues[0]

	err := CompareComments(config, ghIssue, jIssue, gh, jc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(jc.UpdatedComments) != 1 {
		t.Fatalf("Wrong number of updated comments: expected 1; got %d", len(jc.UpdatedComments))
	}
	com := jc.UpdatedComments[0]
	if com.Issue.ID != jIssue.ID {
		t.Errorf("Wrong updated comment issue ID: expected %s; got %s", jIssue.ID, com.Issue.ID)
	}
	if com.GHComment.GetID() != ghComments[ghIssue.GetID()][1].GetID() {
		t.Errorf("Wrong updated comment GH ID: expected %d; got %d", ghComments[ghIssue.GetID()][1].GetID(), com.GHComment.GetID())
	}
	if com.GHComment.GetBody() != ghComments[ghIssue.GetID()][1].GetBody() {
		t.Errorf("Wrong updated comment GH body: expected %d; got %d", ghComments[ghIssue.GetID()][1].GetBody(), com.GHComment.GetBody())
	}

	if len(jc.CreatedComments) != 1 {
		t.Fatalf("Wrong number of created comments: expected 1; got %d", len(jc.CreatedComments))
	}
}

func TestCompareComments_Same(t *testing.T) {
	setup()

	if err := setup(); err != nil {
		t.Fatalf("Error running setup: %v", err)
	}

	gh := testGHClient{}
	jc := &testJIRAClient{}

	config := NewTestConfig()

	ghIssue := ghIssues[1]
	jIssue := jiraIssues[1]

	err := CompareComments(config, ghIssue, jIssue, gh, jc)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(jc.UpdatedComments) != 0 {
		t.Fatalf("Wrong number of updated comments: expected 0; got %d", len(jc.UpdatedComments))
	}

	if len(jc.CreatedComments) != 0 {
		t.Fatalf("Wrong number of created comments: expected 0; got %d", len(jc.CreatedComments))
	}
}
