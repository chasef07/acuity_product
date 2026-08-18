package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type recoveryCandidate struct {
	PracticeID          string `json:"practiceId"`
	CallID              string `json:"callId"`
	ReceiptReference    string `json:"receiptReference"`
	EventType           string `json:"eventType"`
	ErrorCode           string `json:"errorCode"`
	Attempts            int64  `json:"attempts"`
	AgeSeconds          int64  `json:"ageSeconds"`
	RemainingGroupCount int64  `json:"remainingGroupCount"`
}

type stateCount struct {
	State string `json:"state"`
	Count int64  `json:"count"`
}

type recoveryStatus struct {
	PracticeID              string       `json:"practiceId"`
	CallID                  string       `json:"callId"`
	ReceiptReference        string       `json:"receiptReference"`
	EventType               string       `json:"eventType"`
	ErrorCode               string       `json:"errorCode"`
	State                   string       `json:"state"`
	Attempts                int64        `json:"attempts"`
	AgeSeconds              int64        `json:"ageSeconds"`
	DuplicateCount          int64        `json:"duplicateCount"`
	CallState               string       `json:"callState"`
	CallVersion             int64        `json:"callVersion"`
	CallLegStates           []stateCount `json:"callLegStates"`
	CommandStates           []stateCount `json:"commandStates"`
	ActiveReceiptCount      int64        `json:"activeReceiptCount"`
	QuarantinedReceiptCount int64        `json:"quarantinedReceiptCount"`
	RequeueAuditCount       int64        `json:"requeueAuditCount"`
	ResolutionAuditCount    int64        `json:"resolutionAuditCount"`
}

type recoveryResult struct {
	Action                 string            `json:"action"`
	Applied                bool              `json:"applied"`
	Candidate              recoveryCandidate `json:"candidate"`
	Before                 recoveryStatus    `json:"before"`
	After                  *recoveryStatus   `json:"after,omitempty"`
	RequiresTimelineReview bool              `json:"requiresTimelineReview"`
}

type recoveryOptions struct {
	baseURL      string
	practiceID   string
	eventType    string
	errorCode    string
	action       string
	apply        bool
	pollInterval time.Duration
	timeout      time.Duration
	token        string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, http.DefaultClient, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	client httpDoer,
	output io.Writer,
) error {
	options, err := parseRecoveryOptions(args, getenv)
	if err != nil {
		return err
	}
	operator := recoveryHTTPClient{
		baseURL: options.baseURL, token: options.token, client: client,
	}
	candidate, err := operator.candidate(
		ctx, options.practiceID, options.eventType, options.errorCode,
	)
	if err != nil {
		return err
	}
	before, err := operator.status(
		ctx, options.practiceID, candidate.ReceiptReference,
	)
	if err != nil {
		return err
	}
	if err := validateSelectedReceipt(candidate, before); err != nil {
		return err
	}
	result := recoveryResult{
		Action: options.action, Applied: options.apply,
		Candidate: candidate, Before: before,
	}
	if !options.apply {
		return encodeRecoveryResult(output, result)
	}
	if err := preflightRecovery(before); err != nil {
		return err
	}

	switch options.action {
	case "requeue":
		if err := operator.requeue(ctx, options.practiceID, candidate.ReceiptReference); err != nil {
			return err
		}
		after, err := waitForRecovery(
			ctx, operator, options, candidate.ReceiptReference, before,
		)
		if err != nil {
			return err
		}
		result.After = &after
		result.RequiresTimelineReview = callingProjectionChanged(before, after)
	case "resolve":
		if err := operator.resolve(ctx, options.practiceID, candidate.ReceiptReference); err != nil {
			return err
		}
		after, err := operator.status(ctx, options.practiceID, candidate.ReceiptReference)
		if err != nil {
			return err
		}
		if err := validateResolution(before, after); err != nil {
			return err
		}
		result.After = &after
		result.RequiresTimelineReview = callingProjectionChanged(before, after)
	default:
		return errors.New("--apply requires --action=requeue or --action=resolve")
	}
	return encodeRecoveryResult(output, result)
}

func parseRecoveryOptions(
	args []string,
	getenv func(string) string,
) (recoveryOptions, error) {
	var options recoveryOptions
	flags := flag.NewFlagSet("receipt-recovery", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.baseURL, "base-url", "", "portal API base URL")
	flags.StringVar(&options.practiceID, "practice-id", "", "exact Practice ID")
	flags.StringVar(&options.eventType, "event-type", "", "exact audited event type")
	flags.StringVar(&options.errorCode, "error-code", "", "exact audited error code")
	flags.StringVar(&options.action, "action", "inspect", "inspect, requeue, or resolve")
	flags.BoolVar(&options.apply, "apply", false, "perform the one-receipt action")
	flags.DurationVar(&options.pollInterval, "poll-interval", 2*time.Second, "status polling interval")
	flags.DurationVar(&options.timeout, "timeout", 2*time.Minute, "one-receipt convergence timeout")
	if err := flags.Parse(args); err != nil {
		return recoveryOptions{}, fmt.Errorf("parse receipt recovery options: %w", err)
	}
	options.baseURL = strings.TrimRight(strings.TrimSpace(options.baseURL), "/")
	options.practiceID = strings.TrimSpace(options.practiceID)
	options.eventType = strings.TrimSpace(options.eventType)
	options.errorCode = strings.TrimSpace(options.errorCode)
	options.action = strings.ToLower(strings.TrimSpace(options.action))
	options.token = strings.TrimSpace(getenv("OPERATOR_TOKEN"))
	parsedURL, err := url.Parse(options.baseURL)
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") ||
		parsedURL.Host == "" {
		return recoveryOptions{}, errors.New("--base-url must be an absolute HTTP(S) URL")
	}
	if _, err := uuid.Parse(options.practiceID); err != nil {
		return recoveryOptions{}, errors.New("--practice-id must be a UUID")
	}
	if options.eventType == "" || options.errorCode == "" {
		return recoveryOptions{}, errors.New("--event-type and --error-code are required")
	}
	if options.action != "inspect" && options.action != "requeue" && options.action != "resolve" {
		return recoveryOptions{}, errors.New("--action must be inspect, requeue, or resolve")
	}
	if options.apply && options.action == "inspect" {
		return recoveryOptions{}, errors.New("--apply requires --action=requeue or --action=resolve")
	}
	if options.pollInterval <= 0 || options.timeout <= 0 {
		return recoveryOptions{}, errors.New("--poll-interval and --timeout must be positive")
	}
	if options.token == "" {
		return recoveryOptions{}, errors.New("OPERATOR_TOKEN is required")
	}
	return options, nil
}

func validateSelectedReceipt(candidate recoveryCandidate, status recoveryStatus) error {
	if candidate.PracticeID != status.PracticeID || candidate.CallID != status.CallID ||
		candidate.ReceiptReference != status.ReceiptReference ||
		candidate.EventType != status.EventType || candidate.ErrorCode != status.ErrorCode {
		return errors.New("selected receipt status does not match the audited quarantine group")
	}
	return nil
}

func preflightRecovery(status recoveryStatus) error {
	if status.State != "QUARANTINED" {
		return fmt.Errorf("stop: selected receipt state is %s, not QUARANTINED", status.State)
	}
	if status.Attempts <= 0 {
		return errors.New("stop: selected receipt has no projection attempt evidence")
	}
	if unsafeCommandState(status.CommandStates) {
		return errors.New("stop: Call already has an ambiguous or failed provider command")
	}
	return nil
}

func waitForRecovery(
	ctx context.Context,
	operator recoveryHTTPClient,
	options recoveryOptions,
	reference string,
	before recoveryStatus,
) (recoveryStatus, error) {
	deadline := time.NewTimer(options.timeout)
	defer deadline.Stop()
	for {
		after, err := operator.status(ctx, options.practiceID, reference)
		if err != nil {
			return recoveryStatus{}, fmt.Errorf("stop: read one-receipt convergence: %w", err)
		}
		if after.DuplicateCount != before.DuplicateCount {
			return recoveryStatus{}, errors.New("stop: receipt duplicate count changed")
		}
		if unsafeCommandState(after.CommandStates) {
			return recoveryStatus{}, errors.New("stop: provider command became ambiguous or failed")
		}
		switch after.State {
		case "APPLIED":
			if after.RequeueAuditCount != before.RequeueAuditCount+1 {
				return recoveryStatus{}, errors.New("stop: one-receipt requeue audit was not recorded exactly once")
			}
			if after.ActiveReceiptCount > before.ActiveReceiptCount {
				return recoveryStatus{}, errors.New("stop: active receipt backlog grew")
			}
			if after.QuarantinedReceiptCount >= before.QuarantinedReceiptCount {
				return recoveryStatus{}, errors.New("stop: quarantine backlog did not decrease")
			}
			return after, nil
		case "PENDING", "PROCESSING":
		case "QUARANTINED", "UNKNOWN", "FAILED":
			return recoveryStatus{}, fmt.Errorf("stop: receipt converged to %s", after.State)
		default:
			return recoveryStatus{}, fmt.Errorf("stop: unexpected receipt state %q", after.State)
		}
		select {
		case <-ctx.Done():
			return recoveryStatus{}, ctx.Err()
		case <-deadline.C:
			return recoveryStatus{}, errors.New("stop: one-receipt convergence timed out")
		case <-time.After(options.pollInterval):
		}
	}
}

func validateResolution(before recoveryStatus, after recoveryStatus) error {
	if after.ReceiptReference != before.ReceiptReference || after.State != "FAILED" ||
		after.ErrorCode != before.ErrorCode || after.Attempts != before.Attempts ||
		after.DuplicateCount != before.DuplicateCount ||
		after.ResolutionAuditCount != before.ResolutionAuditCount+1 {
		return errors.New("stop: terminal resolution did not preserve receipt failure evidence")
	}
	if after.QuarantinedReceiptCount >= before.QuarantinedReceiptCount {
		return errors.New("stop: quarantine backlog did not decrease after resolution")
	}
	return nil
}

func unsafeCommandState(states []stateCount) bool {
	for _, state := range states {
		if state.Count > 0 && (state.State == "AMBIGUOUS" || state.State == "FAILED") {
			return true
		}
	}
	return false
}

func callingProjectionChanged(before recoveryStatus, after recoveryStatus) bool {
	if before.CallState != after.CallState || before.CallVersion != after.CallVersion ||
		len(before.CallLegStates) != len(after.CallLegStates) {
		return true
	}
	for index := range before.CallLegStates {
		if before.CallLegStates[index] != after.CallLegStates[index] {
			return true
		}
	}
	return false
}

func encodeRecoveryResult(output io.Writer, result recoveryResult) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("encode receipt recovery result: %w", err)
	}
	return nil
}

type recoveryHTTPClient struct {
	baseURL string
	token   string
	client  httpDoer
}

func (client recoveryHTTPClient) candidate(
	ctx context.Context,
	practiceID string,
	eventType string,
	errorCode string,
) (recoveryCandidate, error) {
	query := url.Values{}
	query.Set("eventType", eventType)
	query.Set("errorCode", errorCode)
	var candidate recoveryCandidate
	err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf(
		"/v1/operator/practices/%s/provider-receipts/quarantine-candidate?%s",
		url.PathEscape(practiceID), query.Encode(),
	), nil, &candidate)
	return candidate, err
}

func (client recoveryHTTPClient) status(
	ctx context.Context,
	practiceID string,
	reference string,
) (recoveryStatus, error) {
	var status recoveryStatus
	err := client.doJSON(ctx, http.MethodGet, fmt.Sprintf(
		"/v1/operator/practices/%s/provider-receipts/%s",
		url.PathEscape(practiceID), url.PathEscape(reference),
	), nil, &status)
	return status, err
}

func (client recoveryHTTPClient) requeue(
	ctx context.Context,
	practiceID string,
	reference string,
) error {
	return client.doJSON(ctx, http.MethodPost, fmt.Sprintf(
		"/v1/operator/practices/%s/provider-receipts/%s/requeue",
		url.PathEscape(practiceID), url.PathEscape(reference),
	), map[string]any{}, nil)
}

func (client recoveryHTTPClient) resolve(
	ctx context.Context,
	practiceID string,
	reference string,
) error {
	return client.doJSON(ctx, http.MethodPost, fmt.Sprintf(
		"/v1/operator/practices/%s/provider-receipts/%s/resolve",
		url.PathEscape(practiceID), url.PathEscape(reference),
	), map[string]string{"resolution": "UNSAFE_TO_REPLAY"}, nil)
}

func (client recoveryHTTPClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	body any,
	result any,
) error {
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, requestBody)
	if err != nil {
		return fmt.Errorf("create operator recovery request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("call operator recovery interface: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8*1024))
		return fmt.Errorf("operator recovery interface returned HTTP %d", response.StatusCode)
	}
	if result == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8*1024))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(result); err != nil {
		return fmt.Errorf("decode operator recovery response: %w", err)
	}
	return nil
}
