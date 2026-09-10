package httpapi

import (
	"testing"

	"github.com/hkjang/appstore/internal/model"
)

func TestResubmitAfterEditReopensRejectedApps(t *testing.T) {
	on := model.WorkflowConfig{Enabled: true, ReapprovalAfterEdit: true}
	onWithoutReapproval := model.WorkflowConfig{Enabled: true}
	off := model.WorkflowConfig{ReapprovalAfterEdit: true}
	cases := []struct {
		name   string
		config model.WorkflowConfig
		status string
		want   bool
	}{
		// A rejected app has no other way back into the queue, so an edit
		// always resubmits it while the workflow is on.
		{"rejected app resubmits", on, model.AppStatusRejected, true},
		{"rejected app resubmits without reapproval", onWithoutReapproval, model.AppStatusRejected, true},
		{"published app resubmits on reapproval", on, model.AppStatusPublished, true},
		{"published app stays published without reapproval", onWithoutReapproval, model.AppStatusPublished, false},
		{"app already in review is left alone", on, model.AppStatusPending, false},
		{"draft app is left alone", on, model.AppStatusDraft, false},
		// Submission publishes immediately while the workflow is off, so
		// resubmitting would publish an app a reviewer turned down.
		{"rejected app waits while the workflow is off", off, model.AppStatusRejected, false},
		{"published app waits while the workflow is off", off, model.AppStatusPublished, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := resubmitAfterEdit(testCase.config, testCase.status); got != testCase.want {
				t.Fatalf("resubmitAfterEdit(%+v, %q) = %v, want %v",
					testCase.config, testCase.status, got, testCase.want)
			}
		})
	}
}
