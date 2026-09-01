package workflow

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

type Decision string

const (
	Approve Decision = "approve"
	Reject  Decision = "reject"
)

type Transition struct {
	ReviewStatus string
	AppStatus    string
	NextLevel    int
	Publish      bool
}

func InitialStatus(config model.WorkflowConfig) string {
	if config.Enabled {
		return model.AppStatusPending
	}
	return model.AppStatusPublished
}

func Decide(config model.WorkflowConfig, review model.Review, actorID uuid.UUID, decision Decision, reason string) (Transition, error) {
	if !config.Enabled {
		return Transition{}, errors.New("review workflow is disabled")
	}
	if review.Status != "pending" {
		return Transition{}, errors.New("review is no longer pending")
	}
	if config.PreventSelfApproval && review.SubmitterID == actorID {
		return Transition{}, errors.New("submitters cannot decide their own review")
	}
	switch decision {
	case Reject:
		if config.RejectReasonRequired && strings.TrimSpace(reason) == "" {
			return Transition{}, errors.New("a rejection reason is required")
		}
		return Transition{ReviewStatus: "rejected", AppStatus: model.AppStatusRejected}, nil
	case Approve:
		levels := config.Levels
		if levels < 1 {
			levels = 1
		}
		if review.Level < levels {
			return Transition{ReviewStatus: "approved", AppStatus: model.AppStatusPending, NextLevel: review.Level + 1}, nil
		}
		if config.AutoPublish {
			return Transition{ReviewStatus: "approved", AppStatus: model.AppStatusPublished, Publish: true}, nil
		}
		return Transition{ReviewStatus: "approved", AppStatus: model.AppStatusPending}, nil
	default:
		return Transition{}, errors.New("decision must be approve or reject")
	}
}
