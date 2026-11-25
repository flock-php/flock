package pr_review

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v93/github"
)

type eventKind string

const (
	pullRequestEventKind eventKind = "pull-request"
	workflowRunEventKind eventKind = "workflow-run"
)

type event struct {
	kind     eventKind
	action   string
	number   int
	sender   string
	valid    bool
	workflow workflowRun
	review   reviewSignal
}

type workflowRun struct {
	action      string
	conclusion  string
	event       string
	name        string
	runID       int64
	headSHA     string
	headOwner   string
	headBranch  string
	pullRequest int
}

type reviewSignal struct {
	action   string
	number   int
	reviewID int64
	state    string
}

func parseEvent(data []byte, signalPath string) (event, error) {
	var envelope struct {
		WorkflowRun json.RawMessage `json:"workflow_run"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return event{}, err
	}
	if len(envelope.WorkflowRun) != 0 && string(envelope.WorkflowRun) != "null" {
		return parseWorkflowRunEvent(data, signalPath)
	}

	var source github.PullRequestEvent
	if err := json.Unmarshal(data, &source); err != nil {
		return event{}, err
	}
	pullRequest := source.GetPullRequest()
	if pullRequest == nil || pullRequest.GetNumber() < 1 {
		return event{}, errors.New("missing pull request number")
	}
	if source.GetAction() == "" {
		return event{}, errors.New("missing action")
	}
	return event{
		kind:   pullRequestEventKind,
		action: source.GetAction(),
		number: pullRequest.GetNumber(),
		sender: source.GetSender().GetLogin(),
		valid:  true,
	}, nil
}

func parseWorkflowRunEvent(data []byte, signalPath string) (event, error) {
	var source github.WorkflowRunEvent
	if err := json.Unmarshal(data, &source); err != nil {
		return event{}, err
	}
	result := event{kind: workflowRunEventKind}
	run := source.GetWorkflowRun()
	if run == nil {
		return result, nil
	}
	result.workflow = workflowRun{
		action:     source.GetAction(),
		conclusion: run.GetConclusion(),
		event:      run.GetEvent(),
		name:       run.GetName(),
		runID:      run.GetID(),
		headSHA:    run.GetHeadSHA(),
		headOwner:  run.GetHeadRepository().GetOwner().GetLogin(),
		headBranch: run.GetHeadBranch(),
	}
	if len(run.PullRequests) == 1 {
		result.workflow.pullRequest = run.PullRequests[0].GetNumber()
	}
	result.number = result.workflow.pullRequest
	if signalPath == "" {
		return result, nil
	}
	signalData, err := os.ReadFile(signalPath)
	if err != nil {
		return result, signalFileError("read", signalPath, result.workflow, err)
	}
	var signal github.PullRequestReviewEvent
	if err := json.Unmarshal(signalData, &signal); err != nil {
		return result, signalFileError("parse", signalPath, result.workflow, err)
	}
	review := signal.GetReview()
	result.review = reviewSignal{
		action:   signal.GetAction(),
		number:   signal.GetPullRequest().GetNumber(),
		reviewID: review.GetID(),
		state:    review.GetState(),
	}
	identified := result.workflow.pullRequest > 0 || (result.workflow.headOwner != "" && result.workflow.headBranch != "")
	result.valid = result.workflow.action == "completed" &&
		result.workflow.conclusion == "success" &&
		result.workflow.event == "pull_request_review" &&
		result.workflow.name == signalWorkflowName &&
		result.workflow.runID > 0 &&
		result.workflow.headSHA != "" &&
		identified &&
		result.review.action == "submitted" &&
		result.review.number > 0 &&
		(result.workflow.pullRequest == 0 || result.review.number == result.workflow.pullRequest) &&
		result.review.reviewID > 0 &&
		strings.EqualFold(result.review.state, "changes_requested")
	return result, nil
}

func signalFileError(action, path string, workflow workflowRun, err error) error {
	if workflow.pullRequest > 0 {
		return fmt.Errorf("%s pull request review signal %s for pull request #%d in workflow run %d: %w",
			action, path, workflow.pullRequest, workflow.runID, err)
	}
	return fmt.Errorf("%s pull request review signal %s for workflow run %d: %w", action, path, workflow.runID, err)
}
