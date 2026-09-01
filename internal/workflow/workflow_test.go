package workflow

import (
	"testing"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

func TestWorkflowDefaultOffPublishes(t *testing.T) {
	if got := InitialStatus(model.WorkflowConfig{}); got != model.AppStatusPublished {
		t.Fatalf("status = %q", got)
	}
}

func TestMultiLevelApproval(t *testing.T) {
	submitter, reviewer := uuid.New(), uuid.New()
	config := model.WorkflowConfig{Enabled: true, Levels: 2, AutoPublish: true, PreventSelfApproval: true}
	first, err := Decide(config, model.Review{Status: "pending", Level: 1, SubmitterID: submitter}, reviewer, Approve, "")
	if err != nil || first.NextLevel != 2 || first.Publish {
		t.Fatalf("first = %#v err=%v", first, err)
	}
	second, err := Decide(config, model.Review{Status: "pending", Level: 2, SubmitterID: submitter}, reviewer, Approve, "")
	if err != nil || !second.Publish || second.AppStatus != model.AppStatusPublished {
		t.Fatalf("second = %#v err=%v", second, err)
	}
}

func TestRejectRequiresReasonAndBlocksSelfApproval(t *testing.T) {
	user := uuid.New()
	config := model.WorkflowConfig{Enabled: true, RejectReasonRequired: true, PreventSelfApproval: true}
	review := model.Review{Status: "pending", Level: 1, SubmitterID: user}
	if _, err := Decide(config, review, uuid.New(), Reject, ""); err == nil {
		t.Fatal("expected reason error")
	}
	if _, err := Decide(config, review, user, Approve, ""); err == nil {
		t.Fatal("expected self-approval error")
	}
}
