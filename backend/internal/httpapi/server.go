package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/authn"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

type IdentityAuthenticator interface {
	Authenticate(context.Context, string) (access.Identity, error)
}

type EventStreamer interface {
	Stream(
		http.ResponseWriter,
		*http.Request,
		access.Identity,
		string,
		string,
	) error
	Ready() bool
}

type ServiceAuthenticator interface {
	AuthenticateService(context.Context, string) (access.ServiceIdentity, error)
}

type Config struct {
	AllowedOrigins []string
	AcquireTimeout time.Duration
	RequestTimeout time.Duration
	Observer       observability.Observer
}

type PortalDependencies struct {
	Access               *access.Module
	Authenticator        IdentityAuthenticator
	Calling              *humancalling.Module
	Interactions         *interaction.Module
	Messaging            *messaging.Module
	Work                 *work.Module
	Workspace            *workspace.Module
	ServiceAuthenticator ServiceAuthenticator
}

type RealtimeDependencies struct {
	Access        *access.Module
	Authenticator IdentityAuthenticator
	Events        EventStreamer
}

type Server struct {
	role          string
	config        Config
	pool          *pgxpool.Pool
	access        *access.Module
	authenticator IdentityAuthenticator
	events        EventStreamer
	calling       *humancalling.Module
	interactions  *interaction.Module
	messaging     *messaging.Module
	work          *work.Module
	workspace     *workspace.Module
	serviceAuth   ServiceAuthenticator
	observer      observability.Observer
}

type serverDependencies struct {
	access        *access.Module
	authenticator IdentityAuthenticator
	events        EventStreamer
	calling       *humancalling.Module
	interactions  *interaction.Module
	messaging     *messaging.Module
	work          *work.Module
	workspace     *workspace.Module
	serviceAuth   ServiceAuthenticator
}

func NewPortal(
	config Config,
	pool *pgxpool.Pool,
	dependencies PortalDependencies,
) (http.Handler, error) {
	if dependencies.Access == nil ||
		dependencies.Authenticator == nil ||
		dependencies.Calling == nil ||
		dependencies.Interactions == nil ||
		dependencies.Messaging == nil ||
		dependencies.Work == nil ||
		dependencies.Workspace == nil ||
		dependencies.ServiceAuthenticator == nil {
		return nil, fmt.Errorf("portal dependencies are required")
	}
	return newServer("portal-api", config, pool, serverDependencies{
		access:        dependencies.Access,
		authenticator: dependencies.Authenticator,
		calling:       dependencies.Calling,
		interactions:  dependencies.Interactions,
		messaging:     dependencies.Messaging,
		work:          dependencies.Work,
		workspace:     dependencies.Workspace,
		serviceAuth:   dependencies.ServiceAuthenticator,
	})
}

func NewRealtime(
	config Config,
	pool *pgxpool.Pool,
	dependencies RealtimeDependencies,
) (http.Handler, error) {
	if dependencies.Access == nil ||
		dependencies.Authenticator == nil ||
		dependencies.Events == nil {
		return nil, fmt.Errorf("realtime dependencies are required")
	}
	return newServer("realtime", config, pool, serverDependencies{
		access:        dependencies.Access,
		authenticator: dependencies.Authenticator,
		events:        dependencies.Events,
	})
}

func NewProviderIngress(
	config Config,
	pool *pgxpool.Pool,
	calling *humancalling.Module,
	messagingModule *messaging.Module,
) (http.Handler, error) {
	if calling == nil || messagingModule == nil {
		return nil, fmt.Errorf("provider-ingress dependencies are required")
	}
	return newServer("provider-ingress", config, pool, serverDependencies{
		calling:   calling,
		messaging: messagingModule,
	})
}

func newServer(
	role string,
	config Config,
	pool *pgxpool.Pool,
	dependencies serverDependencies,
) (http.Handler, error) {
	if pool == nil {
		return nil, fmt.Errorf("database pool is required")
	}
	if config.AcquireTimeout <= 0 {
		return nil, fmt.Errorf("positive acquisition timeout is required")
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = config.AcquireTimeout
	}

	server := &Server{
		role:          role,
		config:        config,
		pool:          pool,
		access:        dependencies.access,
		authenticator: dependencies.authenticator,
		events:        dependencies.events,
		calling:       dependencies.calling,
		interactions:  dependencies.interactions,
		messaging:     dependencies.messaging,
		work:          dependencies.work,
		workspace:     dependencies.workspace,
		serviceAuth:   dependencies.serviceAuth,
		observer:      config.Observer,
	}
	generated := api.HandlerWithOptions(server, api.StdHTTPServerOptions{
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
		},
	})
	return server.withRequestMetadata(generated), nil
}

func (server *Server) GetLiveness(w http.ResponseWriter, _ *http.Request) {
	server.writeJSON(w, http.StatusOK, api.Health{
		Role:   healthRole(server.role),
		Status: api.Ok,
	})
}

func (server *Server) GetReadiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), server.config.AcquireTimeout)
	defer cancel()
	if err := server.pool.Ping(ctx); err != nil {
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
		return
	}
	if server.role == "realtime" && !server.events.Ready() {
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
		return
	}
	server.writeJSON(w, http.StatusOK, api.Health{
		Role:   healthRole(server.role),
		Status: api.Ok,
	})
}

func (server *Server) DiscoverAccess(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	discovery, err := server.access.DiscoverActor(ctx, identity)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	response, err := discoveryResponse(discovery)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) InspectSignUpEligibility(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	var body api.SignUpEligibilityRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	eligibility, err := server.access.InspectSignUpEligibility(ctx, string(body.Email))
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, api.SignUpEligibility{
		Kind:  api.SignUpEligibilityKind(eligibility.Kind),
		Email: apiEmail(eligibility.Email),
	})
}

func (server *Server) GetWorkspace(
	w http.ResponseWriter,
	r *http.Request,
	params api.GetWorkspaceParams,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	authorization, err := server.access.ResolveActor(
		ctx,
		identity,
		params.PracticeId.String(),
		params.LocationId.String(),
	)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	response, err := workspaceResponse(authorization)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) AddLocation(
	w http.ResponseWriter,
	r *http.Request,
	practiceID uuid.UUID,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.AddLocationRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	mutation, err := server.access.AddLocation(ctx, access.AddLocationCommand{
		Identity:   identity,
		PracticeID: practiceID.String(),
		Key:        body.Key,
		Name:       body.Name,
	})
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	response, err := locationMutationResponse(mutation)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusCreated, response)
}

func (server *Server) GetEvents(
	w http.ResponseWriter,
	r *http.Request,
	params api.GetEventsParams,
) {
	if server.role != "realtime" {
		server.writeError(w, r, http.StatusNotFound, "NOT_FOUND", "The requested interface is not available in this runtime role.", false)
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	if err := server.events.Stream(
		w,
		r,
		identity,
		params.PracticeId.String(),
		params.LocationId.String(),
	); err != nil {
		server.writeAccessError(w, r, err)
	}
}

func (server *Server) CreateHandoff(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	service, ok := server.authenticateService(w, r)
	if !ok {
		return
	}
	var body api.CreateHandoffRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	input, valid := normalizeCreateHandoffRequest(body)
	if !valid {
		server.writeCallingError(w, r, humancalling.ErrInvalidInput)
		return
	}
	if !service.Allows(access.ServiceCapabilityHumanHandoff) ||
		service.PracticeID != input.practiceID.String() {
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	handoff, err := server.calling.CreateHandoff(ctx, humancalling.CreateHandoffCommand{
		Service:        service,
		OfficeKey:      input.officeKey,
		LocationID:     input.locationID,
		SourceCallID:   input.sourceCallID,
		IdempotencyKey: input.idempotencyKey,
		Contact: humancalling.ContactContext{
			Phone:          input.contact.Phone,
			PhoneSource:    stringValue(input.contact.PhoneSource),
			DisplayName:    stringValue(input.contact.DisplayName),
			NameSource:     stringValue(input.contact.NameSource),
			TransferReason: stringValue(input.contact.TransferReason),
			ReasonSource:   stringValue(input.contact.ReasonSource),
		},
	})
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	handoffID, err := uuid.Parse(handoff.ID)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusCreated, api.Handoff{
		Id:             handoffID,
		SipDestination: handoff.SIPDestination,
		ExpiresAt:      handoff.ExpiresAt,
	})
}

type createHandoffRequest struct {
	contact        api.ContactContextInput
	idempotencyKey string
	locationID     string
	officeKey      string
	practiceID     openapi_types.UUID
	sourceCallID   string
}

func normalizeCreateHandoffRequest(body api.CreateHandoffRequest) (createHandoffRequest, bool) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return createHandoffRequest{}, false
	}
	var input struct {
		Contact        api.ContactContextInput `json:"contact"`
		IdempotencyKey string                  `json:"idempotencyKey"`
		LocationID     *openapi_types.UUID     `json:"locationId"`
		OfficeKey      *string                 `json:"officeKey"`
		PracticeID     openapi_types.UUID      `json:"practiceId"`
		SourceCallID   string                  `json:"sourceCallId"`
	}
	if err := json.Unmarshal(encoded, &input); err != nil ||
		(input.LocationID == nil) == (input.OfficeKey == nil) {
		return createHandoffRequest{}, false
	}
	result := createHandoffRequest{
		contact:        input.Contact,
		idempotencyKey: input.IdempotencyKey,
		practiceID:     input.PracticeID,
		sourceCallID:   input.SourceCallID,
	}
	if input.OfficeKey != nil {
		result.officeKey = *input.OfficeKey
	} else {
		result.locationID = input.LocationID.String()
	}
	return result, true
}

func (server *Server) CreateStaffTask(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	service, ok := server.authenticateService(w, r)
	if !ok {
		return
	}
	var body api.CreateStaffTaskRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	if body.Source != api.Agent {
		server.writeWorkError(w, r, work.ErrInvalidInput)
		return
	}
	var patientID, patientDOB, patientName string
	if body.Patient != nil {
		patientID = stringValue(body.Patient.Id)
		patientDOB = stringValue(body.Patient.Dob)
		patientName = stringValue(body.Patient.Name)
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	task, status, err := server.work.CreateAITask(ctx, work.CreateAITaskCommand{
		Service:                 service,
		OfficeKey:               body.OfficeKey,
		OfficePhone:             body.OfficePhone,
		InboundOfficePhone:      stringValue(body.InboundOfficePhone),
		SourceCallID:            body.CallId,
		IdempotencyKey:          body.IdempotencyKey,
		Phone:                   body.CallerPhone,
		CallerName:              patientName,
		CompatibilityPatientID:  patientID,
		CompatibilityPatientDOB: patientDOB,
		Summary:                 body.Summary,
		Message:                 body.Message,
		Category:                work.TaskCategory(body.Category),
		Urgency:                 work.TaskUrgency(body.Urgency),
	})
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	taskID, err := uuid.Parse(task.ID)
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	httpStatus := http.StatusCreated
	if status == work.TaskDuplicate {
		httpStatus = http.StatusOK
	}
	server.writeJSON(w, httpStatus, api.StaffTaskReceipt{
		Category: api.StaffTaskCategory(task.Category),
		Status:   api.StaffTaskReceiptStatus(status),
		TaskId:   taskID,
		Urgency:  api.StaffTaskUrgency(task.Urgency),
	})
}

func (server *Server) IngestAIInteraction(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	service, ok := server.authenticateService(w, r)
	if !ok {
		return
	}
	var body api.AIInteractionIngestRequest
	if !server.decodeJSONLimit(w, r, &body, 8*1024*1024) {
		return
	}
	command := interaction.IngestCommand{
		Service:         service,
		Kind:            interaction.MessageKind(body.Kind),
		OfficeKey:       stringValue(body.OfficeKey),
		SourceCallID:    body.SourceCallId,
		CallerPhone:     body.CallerPhone,
		OfficePhone:     body.OfficePhone,
		StartedAt:       body.StartedAt,
		EndedAt:         body.EndedAt,
		Status:          interaction.CallStatus(body.Status),
		Summary:         stringValue(body.Summary),
		Transcript:      rawJSON(body.Transcript),
		SummaryPayload:  rawJSON(body.SummaryPayload),
		CloseoutPayload: rawJSON(body.CloseoutPayload),
	}
	if body.AppointmentOutcome != nil {
		command.Appointment = &interaction.AppointmentEvidence{
			Action:             interaction.AppointmentAction(body.AppointmentOutcome.Action),
			OccurredAt:         body.AppointmentOutcome.OccurredAt,
			ExternalPatientID:  stringValue(body.AppointmentOutcome.ExternalPatientId),
			OldAppointmentID:   stringValue(body.AppointmentOutcome.OldAppointmentId),
			NewAppointmentID:   stringValue(body.AppointmentOutcome.NewAppointmentId),
			BookingResult:      rawJSON(body.AppointmentOutcome.BookingResult),
			CancellationResult: rawJSON(body.AppointmentOutcome.CancellationResult),
		}
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	stored, status, err := server.interactions.Ingest(ctx, command)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	interactionID, err := uuid.Parse(stored.ID)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	httpStatus := http.StatusOK
	if status == interaction.StatusCreated {
		httpStatus = http.StatusCreated
	}
	server.writeJSON(w, httpStatus, api.AIInteractionReceipt{
		InteractionId: interactionID,
		Status:        api.AIInteractionReceiptStatus(status),
	})
}

func (server *Server) GetAIInteraction(
	w http.ResponseWriter,
	r *http.Request,
	interactionID openapi_types.UUID,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	stored, err := server.interactions.Read(ctx, identity, interactionID.String())
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	response, err := aiInteractionDetailResponse(stored)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetAIInteractionEvidence(
	w http.ResponseWriter,
	r *http.Request,
	interactionID openapi_types.UUID,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	stored, err := server.interactions.ReadEvidence(ctx, identity, interactionID.String())
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	response, err := aiInteractionEvidenceResponse(stored)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) QueryAIInteractionOutcomes(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.AIOutcomeQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	appointmentAction := interaction.AppointmentAction("")
	if body.AppointmentAction != nil {
		appointmentAction = interaction.AppointmentAction(*body.AppointmentAction)
	}
	skipCounts := body.IncludeCounts != nil && !*body.IncludeCounts
	page, err := server.interactions.QueryOutcomes(
		ctx,
		interaction.QueryOutcomesCommand{
			Identity:          identity,
			PracticeID:        body.PracticeId.String(),
			LocationID:        uuidString(body.LocationId),
			AppointmentAction: appointmentAction,
			SkipCounts:        skipCounts,
			Cursor:            stringValue(body.Cursor),
			Limit:             intValue(body.Limit),
		},
	)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	response, err := aiOutcomePageResponse(page)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) ReviewAIInteractionOutcome(
	w http.ResponseWriter,
	r *http.Request,
	interactionID openapi_types.UUID,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	if err := server.interactions.ReviewOutcome(
		ctx,
		identity,
		interactionID.String(),
	); err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) QueryOperatorAIAnalytics(
	w http.ResponseWriter,
	r *http.Request,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.OperatorAIAnalyticsQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	page, err := server.interactions.QueryAnalytics(
		ctx,
		interaction.QueryAnalyticsCommand{
			Identity:   identity,
			PracticeID: body.PracticeId.String(),
			LocationID: uuidString(body.LocationId),
			Range:      interaction.AnalyticsRange(body.Range),
			Cursor:     stringValue(body.Cursor),
			Limit:      intValue(body.Limit),
		},
	)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	response, err := operatorAIAnalyticsPageResponse(page)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetOperatorAIInteractionAnalytics(
	w http.ResponseWriter,
	r *http.Request,
	interactionID openapi_types.UUID,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	detail, err := server.interactions.ReadOperatorAnalytics(
		ctx,
		identity,
		interactionID.String(),
	)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	response, err := operatorAIInteractionAnalyticsResponse(detail)
	if err != nil {
		server.writeInteractionError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) ReceiveTelnyxWebhook(w http.ResponseWriter, r *http.Request) {
	if server.role != "provider-ingress" {
		server.writeError(w, r, http.StatusNotFound, "NOT_FOUND", "The requested interface is not available in this runtime role.", false)
		return
	}
	startedAt := time.Now()
	outcome := observability.WebhookUnavailable
	defer func() {
		observability.Record(
			server.observer,
			observability.WebhookAcknowledged(outcome, time.Since(startedAt)),
		)
	}()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 256*1024+1))
	if err != nil || len(raw) > 256*1024 {
		outcome = observability.WebhookInvalid
		server.writeError(w, r, http.StatusBadRequest, "INVALID_WEBHOOK", "The provider webhook is invalid.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	receipt, err := server.calling.ReceiveWebhook(
		ctx,
		raw,
		r.Header.Get("telnyx-timestamp"),
		r.Header.Get("telnyx-signature-ed25519"),
	)
	if err != nil {
		if errors.Is(err, humancalling.ErrInvalidWebhook) {
			outcome = observability.WebhookInvalid
			server.writeError(w, r, http.StatusBadRequest, "INVALID_WEBHOOK", "The provider webhook is invalid.", false)
			return
		}
		server.writeCallingError(w, r, err)
		return
	}
	outcome = observability.WebhookAccepted
	if receipt.Duplicate {
		outcome = observability.WebhookDuplicate
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) AcquireSoftphone(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.SoftphoneLeaseRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	state, err := server.calling.AcquireSoftphone(ctx, identity, body.SessionId, body.Takeover)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, softphoneResponse(state))
}

func (server *Server) SetCallingReadiness(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.CallingReadinessRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	state, err := server.calling.SetReadiness(ctx, humancalling.ReadinessCommand{
		Identity:        identity,
		SessionID:       body.SessionId,
		Registered:      body.Registered,
		MicrophoneReady: body.MicrophoneReady,
		AudioReady:      body.AudioReady,
		SessionHealthy:  body.SessionHealthy,
		Available:       body.Available,
	})
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, softphoneResponse(state))
}

func (server *Server) IssueCallingMediaToken(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.MediaTokenRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	token, err := server.calling.IssueMediaJWT(ctx, identity, body.SessionId)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, api.MediaToken{
		Token:     token.Token,
		ExpiresAt: token.ExpiresAt,
	})
}

func (server *Server) GetCallingState(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	state, err := server.calling.ReadCallingState(ctx, identity)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	if r.Header.Get("If-None-Match") == state.ETag {
		w.Header().Set("ETag", state.ETag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", state.ETag)
	response, err := callingStateResponse(state)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetCallingCall(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	call, err := server.calling.ReadCall(ctx, identity, callID.String())
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response, err := callingCallResponse(call)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetCallingEngagementHistory(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
	params api.GetCallingEngagementHistoryParams,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	call, err := server.calling.ReadCall(ctx, identity, callID.String())
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	timeline, err := server.workspace.QueryPhoneTimeline(
		ctx,
		workspace.QueryPhoneTimelineCommand{
			Identity:   identity,
			PracticeID: call.PracticeID,
			Phone:      call.Phone,
			Cursor:     stringValue(params.Cursor),
			Limit:      intValue(params.Limit),
		},
	)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	response, err := conversationTimelineResponse(timeline)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) QueryEngagements(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.EngagementQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	page, err := server.workspace.QueryEngagements(
		ctx,
		workspace.QueryEngagementsCommand{
			Identity:   identity,
			PracticeID: body.PracticeId.String(),
			Phone:      body.Phone,
		},
	)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	response, err := engagementPageResponse(page)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetEngagementTimeline(
	w http.ResponseWriter,
	r *http.Request,
	phone string,
	params api.GetEngagementTimelineParams,
) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	normalized, err := messaging.NormalizePhone(phone)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	timeline, err := server.workspace.QueryPhoneTimeline(
		ctx,
		workspace.QueryPhoneTimelineCommand{
			Identity:   identity,
			PracticeID: params.PracticeId.String(),
			Phone:      normalized,
			Cursor:     stringValue(params.Cursor),
			Limit:      intValue(params.Limit),
		},
	)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	response, err := conversationTimelineResponse(timeline)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) StartOutboundCall(
	w http.ResponseWriter,
	r *http.Request,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.StartOutboundCallRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	call, err := server.calling.StartOutboundCall(
		ctx,
		humancalling.StartOutboundCallCommand{
			Identity:       identity,
			SessionID:      body.SessionId,
			IdempotencyKey: body.IdempotencyKey,
			TaskID:         uuidString(body.TaskId),
			PracticeID:     uuidString(body.PracticeId),
			LocationID:     uuidString(body.LocationId),
			Destination:    stringValue(body.Destination),
		},
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response, err := callingCallResponse(call)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusCreated, response)
}

func (server *Server) ConfirmCallingMediaReady(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.ConfirmCallingMediaRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	call, err := server.calling.ConfirmOutboundMedia(
		ctx,
		humancalling.ConfirmOutboundMediaCommand{
			Identity:   identity,
			SessionID:  body.SessionId,
			CallID:     callID.String(),
			MediaToken: body.MediaToken,
		},
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response, err := callingCallResponse(call)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetTaskOutboundEligibility(
	w http.ResponseWriter,
	r *http.Request,
	taskID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	eligibility, err := server.calling.TaskOutboundEligibility(
		ctx,
		identity,
		taskID.String(),
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, api.OutboundCallEligibility{
		Eligible: eligibility.Eligible,
		Reason:   eligibility.Reason,
	})
}

func (server *Server) RetryOutboundCall(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.RetryOutboundCallRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	previous, err := server.calling.ReadCall(ctx, identity, callID.String())
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	command := humancalling.StartOutboundCallCommand{
		Identity:       identity,
		SessionID:      body.SessionId,
		IdempotencyKey: body.IdempotencyKey,
		RetryOfCallID:  previous.ID,
	}
	switch previous.EntryPoint {
	case humancalling.CallEntryTask:
		command.TaskID = previous.TaskID
	case humancalling.CallEntryStandalone:
		command.PracticeID = previous.PracticeID
		command.LocationID = previous.LocationID
		command.Destination = previous.Phone
	default:
		server.writeCallingError(w, r, humancalling.ErrConflict)
		return
	}
	call, err := server.calling.StartOutboundCall(ctx, command)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response, err := callingCallResponse(call)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusCreated, response)
}

func (server *Server) IssueCallingVoicemailPlayback(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	capability, err := server.calling.IssueVoicemailPlayback(
		ctx,
		identity,
		callID.String(),
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, api.VoicemailPlaybackCapability{
		Token:     capability.Token,
		ExpiresAt: capability.ExpiresAt,
	})
}

func (server *Server) IssueCallingRecordingPlayback(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	capability, err := server.calling.IssueCallRecordingPlayback(
		ctx,
		identity,
		callID.String(),
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, api.RecordingPlaybackCapability{
		Token:     capability.Token,
		ExpiresAt: capability.ExpiresAt,
	})
}

func (server *Server) GetCallingVoicemailPlayback(
	w http.ResponseWriter,
	r *http.Request,
	token string,
	params api.GetCallingVoicemailPlaybackParams,
) {
	if !server.portalOnly(w, r) {
		return
	}
	server.streamCallingPlayback(
		w, r, token, stringValue(params.Range), humancalling.PlaybackVoicemail,
		server.calling.OpenVoicemailPlayback,
	)
}

func (server *Server) GetCallingRecordingPlayback(
	w http.ResponseWriter,
	r *http.Request,
	token string,
	params api.GetCallingRecordingPlaybackParams,
) {
	if !server.portalOnly(w, r) {
		return
	}
	server.streamCallingPlayback(
		w, r, token, stringValue(params.Range), humancalling.PlaybackCallRecording,
		server.calling.OpenCallRecordingPlayback,
	)
}

func (server *Server) streamCallingPlayback(
	w http.ResponseWriter,
	r *http.Request,
	token string,
	rangeHeader string,
	kind humancalling.PlaybackKind,
	open func(
		context.Context,
		context.Context,
		string,
		string,
	) (humancalling.PlaybackContent, error),
) {
	ctx, cancel := server.requestContext(r)
	defer cancel()
	content, err := open(
		ctx,
		r.Context(),
		token,
		rangeHeader,
	)
	if err != nil {
		server.writePlaybackError(w, r, err, kind)
		return
	}
	if err := content.Validate(rangeHeader); err != nil {
		if content.Body != nil {
			_ = content.Body.Close()
		}
		content.Complete(err)
		server.writePlaybackError(w, r, err, kind)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", safeAudioContentType(content.ContentType))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if length, ok := safeContentLength(content.ContentLength); ok {
		w.Header().Set("Content-Length", length)
	}
	if content.StatusCode == http.StatusPartialContent {
		w.Header().Set("Content-Range", content.ContentRange)
	}
	w.WriteHeader(content.StatusCode)
	_, copyErr := io.Copy(w, content.Body)
	closeErr := content.Body.Close()
	streamErr := copyErr
	if streamErr == nil {
		streamErr = closeErr
	}
	content.Complete(streamErr)
	if copyErr != nil {
		panic(http.ErrAbortHandler)
	}
}

func safeAudioContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch value {
	case "audio/mpeg", "audio/mp3", "audio/wav", "audio/x-wav":
		return value
	default:
		return "audio/mpeg"
	}
}

func safeContentLength(value string) (string, bool) {
	value = strings.TrimSpace(value)
	length, err := strconv.ParseInt(value, 10, 64)
	return value, err == nil && length >= 0
}

func (server *Server) RequestCallingHangup(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.CallingControlRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	call, err := server.calling.RequestHangup(
		ctx,
		identity,
		body.SessionId,
		callID.String(),
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response, err := callingCallResponse(call)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusAccepted, response)
}

func (server *Server) RecordCallingDisposition(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.CallingDispositionRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	result, err := server.calling.RecordDisposition(
		ctx,
		identity,
		body.SessionId,
		callID.String(),
		humancalling.Disposition(body.Outcome),
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	callResponse, err := callingCallResponse(result.Call)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response := api.CallingDispositionResult{Call: callResponse}
	if result.TaskID != "" {
		taskID, err := uuid.Parse(result.TaskID)
		if err != nil {
			server.writeCallingError(w, r, err)
			return
		}
		response.TaskId = &taskID
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetCallingCallHistory(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
	params api.GetCallingCallHistoryParams,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	call, err := server.calling.ReadCall(ctx, identity, callID.String())
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	history, err := server.calling.QueryCallHistory(
		ctx,
		humancalling.CallHistoryQuery{
			Identity:      identity,
			PracticeID:    call.PracticeID,
			Phone:         call.Phone,
			CurrentCallID: call.ID,
			Cursor:        stringValue(params.Cursor),
		},
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response, err := callHistoryResponse(history)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) QueryTasks(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.taskIdentity(w, r)
	if !ok {
		return
	}
	var body api.TaskQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ordering := work.TaskOrderingPriority
	if body.Ordering != nil {
		ordering = work.TaskOrdering(*body.Ordering)
	}
	state := work.TaskOpen
	if body.State != nil {
		state = work.TaskState(*body.State)
	}
	folder := work.TaskFolder("")
	if body.Folder != nil {
		folder = work.TaskFolder(*body.Folder)
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	page, err := server.workspace.QueryTasks(ctx, workspace.QueryTasksCommand{
		Identity:   identity,
		PracticeID: body.PracticeId.String(),
		LocationID: uuidString(body.LocationId),
		Search:     stringValue(body.Search),
		State:      state,
		Ordering:   ordering,
		Folder:     folder,
		Cursor:     stringValue(body.Cursor),
		Limit:      intValue(body.Limit),
	})
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	response, err := taskPageResponse(page)
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) ReadTask(
	w http.ResponseWriter,
	r *http.Request,
	taskID openapi_types.UUID,
) {
	identity, ok := server.taskIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	task, err := server.workspace.ReadTask(ctx, identity, taskID.String())
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	response, err := taskResponse(task)
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) RenameTask(
	w http.ResponseWriter,
	r *http.Request,
	taskID openapi_types.UUID,
) {
	identity, ok := server.taskIdentity(w, r)
	if !ok {
		return
	}
	var body api.RenameTaskRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	task, err := server.work.RenameTask(ctx, work.RenameTaskCommand{
		Identity:        identity,
		TaskID:          taskID.String(),
		ExpectedVersion: body.ExpectedVersion,
		Title:           body.Title,
	})
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	response, err := taskResponse(task)
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) CompleteTask(
	w http.ResponseWriter,
	r *http.Request,
	taskID openapi_types.UUID,
) {
	identity, ok := server.taskIdentity(w, r)
	if !ok {
		return
	}
	var body api.TaskTransitionRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	task, err := server.work.CompleteTask(ctx, work.CompleteTaskCommand{
		Identity:        identity,
		TaskID:          taskID.String(),
		ExpectedVersion: body.ExpectedVersion,
	})
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	response, err := taskResponse(task)
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) ReopenTask(
	w http.ResponseWriter,
	r *http.Request,
	taskID openapi_types.UUID,
) {
	identity, ok := server.taskIdentity(w, r)
	if !ok {
		return
	}
	var body api.TaskTransitionRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	task, err := server.work.ReopenTask(ctx, work.ReopenTaskCommand{
		Identity:        identity,
		TaskID:          taskID.String(),
		ExpectedVersion: body.ExpectedVersion,
	})
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	response, err := taskResponse(task)
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetTaskCallHistory(
	w http.ResponseWriter,
	r *http.Request,
	taskID openapi_types.UUID,
	params api.GetTaskCallHistoryParams,
) {
	identity, ok := server.taskIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	task, err := server.workspace.ReadTask(ctx, identity, taskID.String())
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	history, err := server.calling.QueryCallHistory(
		ctx,
		humancalling.CallHistoryQuery{
			Identity:          identity,
			PracticeID:        task.PracticeID,
			Phone:             task.Phone,
			OriginatingCallID: task.CallID,
			Cursor:            stringValue(params.Cursor),
		},
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response, err := callHistoryResponse(history)
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetTaskEngagementHistory(
	w http.ResponseWriter,
	r *http.Request,
	taskID openapi_types.UUID,
	params api.GetTaskEngagementHistoryParams,
) {
	identity, ok := server.taskIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	task, err := server.workspace.ReadTask(ctx, identity, taskID.String())
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	timeline, err := server.workspace.QueryPhoneTimeline(
		ctx,
		workspace.QueryPhoneTimelineCommand{
			Identity:   identity,
			PracticeID: task.PracticeID,
			Phone:      task.Phone,
			Cursor:     stringValue(params.Cursor),
			Limit:      intValue(params.Limit),
		},
	)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	response, err := conversationTimelineResponse(timeline)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) QueryMessageThreads(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.MessageThreadQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	page, err := server.messaging.QueryThreads(
		ctx,
		messaging.QueryThreadsCommand{
			Identity:   identity,
			PracticeID: body.PracticeId.String(),
			LocationID: uuidString(body.LocationId),
			Search:     stringValue(body.Search),
			Cursor:     stringValue(body.Cursor),
			Limit:      intValue(body.Limit),
		},
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	response, err := messageThreadPageResponse(page)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) GetMessageThreadTimeline(
	w http.ResponseWriter,
	r *http.Request,
	threadID openapi_types.UUID,
	params api.GetMessageThreadTimelineParams,
) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	page, err := server.workspace.QueryTimeline(
		ctx,
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: threadID.String(),
			Cursor:   stringValue(params.Cursor),
			Limit:    intValue(params.Limit),
		},
	)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	response, err := conversationTimelineResponse(page)
	if err != nil {
		server.writeWorkspaceError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) MarkMessageThreadRead(
	w http.ResponseWriter,
	r *http.Request,
	threadID openapi_types.UUID,
) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.MarkMessageThreadReadRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	if err := server.messaging.MarkRead(ctx, messaging.MarkReadCommand{
		Identity: identity,
		ThreadID: threadID.String(),
	}); err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) SendMessage(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.SendMessageRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	message, status, err := server.messaging.Send(ctx, messaging.SendCommand{
		Identity:       identity,
		PracticeID:     body.PracticeId.String(),
		LocationID:     body.LocationId.String(),
		ThreadID:       uuidString(body.ThreadId),
		Destination:    stringValue(body.Destination),
		Body:           body.Body,
		TaskID:         uuidString(body.TaskId),
		AttachmentID:   uuidString(body.AttachmentId),
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	converted, err := messageResponse(message)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	httpStatus := http.StatusCreated
	if status == messaging.MessageDuplicate {
		httpStatus = http.StatusOK
	}
	server.writeJSON(w, httpStatus, api.MessageReceipt{
		Message: converted,
		Status:  api.MessageReceiptStatus(status),
	})
}

func (server *Server) UploadMessageAttachment(
	w http.ResponseWriter,
	r *http.Request,
) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.UploadMessageAttachmentRequest
	if !server.decodeJSONLimit(w, r, &body, 900*1024) {
		return
	}
	content, err := base64.StdEncoding.DecodeString(body.ContentBase64)
	if err != nil {
		server.writeMessagingError(w, r, messaging.ErrInvalidInput)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	attachment, err := server.messaging.UploadAttachment(
		ctx,
		messaging.UploadAttachmentCommand{
			Identity:     identity,
			PracticeID:   body.PracticeId.String(),
			LocationID:   body.LocationId.String(),
			FileName:     body.FileName,
			DeclaredType: string(body.ContentType),
			Content:      content,
		},
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	response, err := messageAttachmentResponse(attachment)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusCreated, response)
}

func (server *Server) GetMessageAttachment(
	w http.ResponseWriter,
	r *http.Request,
	attachmentID openapi_types.UUID,
) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	content, err := server.messaging.OpenAttachment(
		ctx,
		identity,
		attachmentID.String(),
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	writeAttachmentContent(w, content)
}

func (server *Server) RetryInboundMessageAttachment(
	w http.ResponseWriter,
	r *http.Request,
	attachmentID openapi_types.UUID,
) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.RetryMessageAttachmentRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	attachment, err := server.messaging.RetryAttachment(
		ctx,
		messaging.RetryAttachmentCommand{
			Identity:     identity,
			AttachmentID: attachmentID.String(),
		},
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	response, err := messageAttachmentResponse(attachment)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) SendMessageAgain(
	w http.ResponseWriter,
	r *http.Request,
	messageID openapi_types.UUID,
) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.SendMessageAgainRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	message, status, err := server.messaging.SendAgain(
		ctx,
		messaging.SendAgainCommand{
			Identity:                  identity,
			MessageID:                 messageID.String(),
			IdempotencyKey:            body.IdempotencyKey,
			DuplicateRiskAcknowledged: body.DuplicateRiskAcknowledged,
		},
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	response, err := messageResponse(message)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	httpStatus := http.StatusCreated
	if status == messaging.MessageDuplicate {
		httpStatus = http.StatusOK
	}
	server.writeJSON(w, httpStatus, api.MessageReceipt{
		Message: response,
		Status:  api.MessageReceiptStatus(status),
	})
}

func (server *Server) CreateMessageFollowUpTask(
	w http.ResponseWriter,
	r *http.Request,
	messageID openapi_types.UUID,
) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.CreateMessageFollowUpTaskRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	task, status, err := server.messaging.CreateFollowUpTask(
		ctx,
		messaging.CreateFollowUpTaskCommand{
			Identity:  identity,
			MessageID: messageID.String(),
			Title:     stringValue(body.Title),
		},
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	response, err := taskResponse(task)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	httpStatus := http.StatusCreated
	if status == work.TaskDuplicate {
		httpStatus = http.StatusOK
	}
	server.writeJSON(w, httpStatus, response)
}

func (server *Server) ReceiveTelnyxMessagingWebhook(
	w http.ResponseWriter,
	r *http.Request,
) {
	server.receiveMessagingWebhook(w, r, "")
}

func (server *Server) ReceiveCorrelatedTelnyxMessagingWebhook(
	w http.ResponseWriter,
	r *http.Request,
	callbackToken string,
) {
	server.receiveMessagingWebhook(w, r, callbackToken)
}

func (server *Server) GetProviderMessageMedia(
	w http.ResponseWriter,
	r *http.Request,
	attachmentID openapi_types.UUID,
	params api.GetProviderMessageMediaParams,
) {
	if server.role != "provider-ingress" || server.messaging == nil {
		server.writeError(w, r, http.StatusNotFound, "NOT_FOUND", "The requested interface is not available in this runtime role.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	content, err := server.messaging.OpenProviderAttachment(
		ctx,
		attachmentID.String(),
		params.Expires,
		params.Signature,
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	writeAttachmentContent(w, content)
}

func (server *Server) receiveMessagingWebhook(
	w http.ResponseWriter,
	r *http.Request,
	callbackToken string,
) {
	if server.role != "provider-ingress" || server.messaging == nil {
		server.writeError(w, r, http.StatusNotFound, "NOT_FOUND", "The requested interface is not available in this runtime role.", false)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		server.writeError(w, r, http.StatusBadRequest, "INVALID_WEBHOOK", "The provider webhook is invalid.", false)
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	if _, err := server.messaging.ReceiveWebhook(
		ctx,
		callbackToken,
		raw,
		r.Header.Get("telnyx-timestamp"),
		r.Header.Get("telnyx-signature-ed25519"),
	); err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (server *Server) GetOperatorCallingTimeline(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	timeline, err := server.calling.ReadOperatorTimeline(ctx, identity, callID.String())
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response, err := operatorTimelineResponse(timeline)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) RequeueOperatorProviderReceipt(
	w http.ResponseWriter,
	r *http.Request,
	practiceID openapi_types.UUID,
	receiptReference string,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	decodedReference, err := base64.RawURLEncoding.DecodeString(receiptReference)
	if err != nil || len(decodedReference) != 32 {
		server.writeCallingError(w, r, humancalling.ErrInvalidInput)
		return
	}
	var body api.ProviderReceiptRecoveryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.requestContext(r)
	defer cancel()
	result, err := server.calling.RequeueQuarantinedReceipt(
		ctx,
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         identity,
			PracticeID:       practiceID.String(),
			ReceiptReference: receiptReference,
		},
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, api.ProviderReceiptRecovery{
		ReceiptReference: receiptReference,
		State:            api.ProviderReceiptRecoveryState(result.State),
	})
}

func (server *Server) portalOnly(w http.ResponseWriter, r *http.Request) bool {
	if server.role == "portal-api" {
		return true
	}
	server.writeError(w, r, http.StatusNotFound, "NOT_FOUND", "The requested interface is not available in this runtime role.", false)
	return false
}

func (server *Server) callingIdentity(
	w http.ResponseWriter,
	r *http.Request,
) (access.Identity, bool) {
	if !server.portalOnly(w, r) {
		return access.Identity{}, false
	}
	return server.authenticate(w, r)
}

func (server *Server) taskIdentity(
	w http.ResponseWriter,
	r *http.Request,
) (access.Identity, bool) {
	if !server.portalOnly(w, r) {
		return access.Identity{}, false
	}
	return server.authenticate(w, r)
}

func (server *Server) messagingIdentity(
	w http.ResponseWriter,
	r *http.Request,
) (access.Identity, bool) {
	if !server.portalOnly(w, r) || server.messaging == nil {
		return access.Identity{}, false
	}
	return server.authenticate(w, r)
}

func (server *Server) authenticate(w http.ResponseWriter, r *http.Request) (access.Identity, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || strings.Contains(strings.TrimPrefix(header, "Bearer "), " ") {
		server.writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid credential is required.", false)
		return access.Identity{}, false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	identity, err := server.authenticator.Authenticate(r.Context(), token)
	if err != nil {
		server.writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid credential is required.", false)
		return access.Identity{}, false
	}
	return identity, true
}

func (server *Server) authenticateService(
	w http.ResponseWriter,
	r *http.Request,
) (access.ServiceIdentity, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") ||
		strings.Contains(strings.TrimPrefix(header, "Bearer "), " ") {
		server.writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid credential is required.", false)
		return access.ServiceIdentity{}, false
	}
	identity, err := server.serviceAuth.AuthenticateService(
		r.Context(),
		strings.TrimPrefix(header, "Bearer "),
	)
	if err != nil {
		server.writeError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid credential is required.", false)
		return access.ServiceIdentity{}, false
	}
	return identity, true
}

func (server *Server) requestContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), server.config.RequestTimeout)
}

func (server *Server) decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	return server.decodeJSONLimit(w, r, target, 32*1024)
}

func (server *Server) decodeJSONLimit(
	w http.ResponseWriter,
	r *http.Request,
	target any,
	maximumBytes int64,
) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "A JSON request body is required.", false)
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maximumBytes))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The JSON request body is invalid.", false)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The JSON request body is invalid.", false)
		return false
	}
	return true
}

func writeAttachmentContent(
	w http.ResponseWriter,
	content messaging.AttachmentContent,
) {
	w.Header().Set("Content-Type", content.Attachment.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content.Content)))
	disposition := "inline"
	if content.Attachment.ContentType == "application/pdf" {
		disposition = "attachment"
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	w.Header().Set(
		"Content-Disposition",
		disposition+`; filename="`+safeAttachmentFileName(
			content.Attachment.ContentType,
		)+`"`,
	)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content.Content)
}

func safeAttachmentFileName(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return "attachment.jpg"
	case "image/png":
		return "attachment.png"
	case "image/gif":
		return "attachment.gif"
	case "image/webp":
		return "attachment.webp"
	default:
		return "attachment.pdf"
	}
}

func (server *Server) writeAccessError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, access.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, access.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	default:
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
	}
}

func (server *Server) writeCallingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, humancalling.ErrInvalidInput),
		errors.Is(err, humancalling.ErrInvalidWebhook):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, humancalling.ErrOccupied):
		server.writeError(w, r, http.StatusConflict, "CALL_OCCUPIED", "Another Call is pending or active.", false)
	case errors.Is(err, humancalling.ErrDenied),
		errors.Is(err, humancalling.ErrInvalidHandoff),
		errors.Is(err, humancalling.ErrIneligible):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, humancalling.ErrConflict),
		errors.Is(err, humancalling.ErrExpired):
		server.writeError(w, r, http.StatusConflict, "CALL_CONFLICT", "The Call state changed. Refresh and try again.", false)
	default:
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
	}
}

func (server *Server) writePlaybackError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	kind humancalling.PlaybackKind,
) {
	var unavailable *humancalling.RecordingUnavailableError
	if !errors.As(err, &unavailable) {
		server.writeCallingError(w, r, err)
		return
	}
	label, title, code := playbackPresentation(kind)
	status := http.StatusServiceUnavailable
	message := title + " playback is temporarily unavailable. Try again."
	retryable := true
	switch unavailable.Reason {
	case humancalling.RecordingNotFound:
		status = http.StatusNotFound
		message = "This " + label + " is no longer available."
		retryable = false
	case humancalling.RecordingProviderAuth:
		message = title + " playback is temporarily unavailable. Contact support if this continues."
		retryable = false
	case humancalling.RecordingRateLimited:
		message = title + " playback is temporarily busy. Try again shortly."
		if unavailable.RetryAfter != "" {
			w.Header().Set("Retry-After", unavailable.RetryAfter)
		}
	case humancalling.RecordingProviderTimeout:
		status = http.StatusGatewayTimeout
		message = title + " playback timed out. Try again."
	}
	server.writeError(
		w,
		r,
		status,
		code,
		message,
		retryable,
	)
}

func playbackPresentation(kind humancalling.PlaybackKind) (string, string, string) {
	if kind == humancalling.PlaybackCallRecording {
		return "call recording", "Call recording", "CALL_RECORDING_UNAVAILABLE"
	}
	return "voicemail", "Voicemail", "VOICEMAIL_UNAVAILABLE"
}

func (server *Server) writeWorkError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, work.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, work.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, work.ErrConflict):
		server.writeError(w, r, http.StatusConflict, "TASK_CONFLICT", "The Task state changed. Refresh and try again.", false)
	default:
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
	}
}

func (server *Server) writeWorkspaceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, workspace.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, workspace.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	default:
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
	}
}

func (server *Server) writeInteractionError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, interaction.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, interaction.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, interaction.ErrConflict):
		server.writeError(w, r, http.StatusConflict, "INTERACTION_CONFLICT", "The source call conflicts with the stored Interaction.", false)
	default:
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
	}
}

func (server *Server) writeMessagingError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, messaging.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, messaging.ErrDenied):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, messaging.ErrBlocked):
		server.writeError(w, r, http.StatusConflict, "MESSAGE_BLOCKED", "This conversation has opted out of outbound Messages.", false)
	case errors.Is(err, messaging.ErrConflict):
		server.writeError(w, r, http.StatusConflict, "MESSAGE_CONFLICT", "The Message state changed. Refresh and try again.", false)
	default:
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
	}
}

func (server *Server) writeError(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code string,
	message string,
	retryable bool,
) {
	var envelope api.ErrorEnvelope
	envelope.Error.Code = code
	envelope.Error.Message = message
	envelope.Error.CorrelationId = correlationID(r.Context())
	envelope.Error.Retryable = retryable
	server.writeJSON(w, status, envelope)
}

func (server *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (server *Server) withRequestMetadata(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlation := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
		if correlation == "" || len(correlation) > 128 {
			correlation = newCorrelationID()
		}
		ctx := context.WithValue(r.Context(), correlationContextKey{}, correlation)
		w.Header().Set("X-Correlation-ID", correlation)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if origin := r.Header.Get("Origin"); slices.Contains(server.config.AllowedOrigins, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-None-Match, X-Correlation-ID")
			w.Header().Set("Access-Control-Expose-Headers", "ETag")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		route := observability.AvailabilityRoute("")
		if server.role == "portal-api" {
			route = availabilityRoute(r.Method, r.URL.Path)
		}
		if route == "" {
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		response := &statusResponseWriter{ResponseWriter: w}
		started := time.Now()
		completed := false
		defer func() {
			outcome, failureStage := availabilityResult(route, response.statusCode())
			if !completed {
				outcome = observability.AvailabilityUnavailable
				failureStage = observability.FailureHandler
			}
			observability.Record(server.observer, observability.BackendRequest(
				route,
				outcome,
				failureStage,
				time.Since(started),
			))
		}()
		next.ServeHTTP(response, r.WithContext(ctx))
		completed = true
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *statusResponseWriter) statusCode() int {
	if writer.status == 0 {
		return http.StatusOK
	}
	return writer.status
}

func availabilityRoute(method string, path string) observability.AvailabilityRoute {
	if method != http.MethodGet {
		return ""
	}
	switch path {
	case string(observability.AvailabilityAccess):
		return observability.AvailabilityAccess
	case string(observability.AvailabilityCallingState):
		return observability.AvailabilityCallingState
	default:
		return ""
	}
}

func availabilityResult(
	route observability.AvailabilityRoute,
	status int,
) (observability.AvailabilityOutcome, observability.FailureStage) {
	available := status == http.StatusOK ||
		(route == observability.AvailabilityCallingState && status == http.StatusNotModified)
	if available {
		return observability.AvailabilityAvailable, observability.FailureNone
	}
	switch status {
	case http.StatusUnauthorized:
		return observability.AvailabilityUnavailable, observability.FailureAuthentication
	case http.StatusForbidden:
		return observability.AvailabilityUnavailable, observability.FailureAuthorization
	}
	if status >= 500 {
		return observability.AvailabilityUnavailable, observability.FailureDependency
	}
	return observability.AvailabilityUnavailable, observability.FailureHandler
}

type correlationContextKey struct{}

func correlationID(ctx context.Context) string {
	value, _ := ctx.Value(correlationContextKey{}).(string)
	if value == "" {
		return newCorrelationID()
	}
	return value
}

func newCorrelationID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "correlation-unavailable"
	}
	return hex.EncodeToString(value)
}

func healthRole(role string) api.HealthRole {
	switch role {
	case "portal-api":
		return api.PortalApi
	case "provider-ingress":
		return api.ProviderIngress
	default:
		return api.Realtime
	}
}

func discoveryResponse(discovery access.Discovery) (api.AccessDiscovery, error) {
	response := api.AccessDiscovery{
		Actor:            actorResponse(discovery.Actor),
		PlatformOperator: discovery.PlatformOperator,
		Practices:        make([]api.PracticeAccess, 0, len(discovery.Practices)),
	}
	for _, practice := range discovery.Practices {
		practiceResponse, err := practiceResponse(practice.Practice)
		if err != nil {
			return api.AccessDiscovery{}, err
		}
		item := api.PracticeAccess{
			Id:             practiceResponse.Id,
			Name:           practiceResponse.Name,
			Version:        practiceResponse.Version,
			Locations:      []api.Location{},
			CallingEnabled: practice.CallingEnabled,
		}
		if practice.Membership != nil {
			membership, err := membershipResponse(*practice.Membership)
			if err != nil {
				return api.AccessDiscovery{}, err
			}
			item.Membership = &membership
		}
		for _, location := range practice.Locations {
			converted, err := locationResponse(location)
			if err != nil {
				return api.AccessDiscovery{}, err
			}
			item.Locations = append(item.Locations, converted)
		}
		response.Practices = append(response.Practices, item)
	}
	return response, nil
}

func workspaceResponse(authorization access.Authorization) (api.WorkspaceSnapshot, error) {
	if authorization.ActiveLocation == nil {
		return api.WorkspaceSnapshot{}, access.ErrDenied
	}
	practice, err := practiceResponse(authorization.Practice)
	if err != nil {
		return api.WorkspaceSnapshot{}, err
	}
	location, err := locationResponse(*authorization.ActiveLocation)
	if err != nil {
		return api.WorkspaceSnapshot{}, err
	}
	var membership *api.Membership
	if authorization.Membership.ID != "" {
		converted, err := membershipResponse(authorization.Membership)
		if err != nil {
			return api.WorkspaceSnapshot{}, err
		}
		membership = &converted
	}
	response := api.WorkspaceSnapshot{
		SchemaVersion:    api.N20260724,
		Version:          authorization.Practice.Version,
		State:            api.EMPTY,
		Actor:            actorResponse(authorization.Actor),
		Practice:         practice,
		Location:         location,
		Membership:       membership,
		PlatformOperator: authorization.PlatformOperator,
		Navigation: []api.NavigationItem{
			{Id: api.Tasks, Label: "Tasks", Enabled: true},
			{Id: api.Messages, Label: "Messages", Enabled: true},
			{Id: api.CallCenter, Label: "Call Center", Enabled: false},
			{Id: api.Recordings, Label: "Recordings", Enabled: false},
			{Id: api.Settings, Label: "Settings", Enabled: false},
		},
	}
	return response, nil
}

func locationMutationResponse(mutation access.LocationMutation) (api.LocationMutation, error) {
	location, err := locationResponse(mutation.Location)
	if err != nil {
		return api.LocationMutation{}, err
	}
	auditID, err := uuid.Parse(mutation.Audit.ID)
	if err != nil {
		return api.LocationMutation{}, err
	}
	practiceID, err := uuid.Parse(mutation.Audit.PracticeID)
	if err != nil {
		return api.LocationMutation{}, err
	}
	return api.LocationMutation{
		Location:        location,
		PracticeVersion: mutation.PracticeVersion,
		Audit: api.AuditEvent{
			Id:           auditID,
			ActorSubject: mutation.Audit.ActorSubject,
			PracticeId:   practiceID,
			Action:       mutation.Audit.Action,
			CreatedAt:    mutation.Audit.CreatedAt,
		},
	}, nil
}

func actorResponse(actor access.Actor) api.Actor {
	return api.Actor{
		Subject: actor.Subject,
		Email:   apiEmail(actor.Email),
		Type:    api.ActorType(actor.Type),
	}
}

func practiceResponse(practice access.Practice) (api.Practice, error) {
	id, err := uuid.Parse(practice.ID)
	if err != nil {
		return api.Practice{}, err
	}
	return api.Practice{Id: id, Name: practice.Name, Version: practice.Version}, nil
}

func locationResponse(location access.Location) (api.Location, error) {
	id, err := uuid.Parse(location.ID)
	if err != nil {
		return api.Location{}, err
	}
	return api.Location{Id: id, Name: location.Name}, nil
}

func membershipResponse(membership access.Membership) (api.Membership, error) {
	id, err := uuid.Parse(membership.ID)
	if err != nil {
		return api.Membership{}, err
	}
	return api.Membership{
		Id:            id,
		Role:          api.MembershipRole(membership.Role),
		LocationScope: api.MembershipLocationScope(membership.LocationScope),
	}, nil
}

func softphoneResponse(state humancalling.SoftphoneState) api.SoftphoneState {
	return api.SoftphoneState{
		SessionId:            state.SessionID,
		LeaseExpiresAt:       state.LeaseExpiresAt,
		Owner:                state.Owner,
		Available:            state.Available,
		ActiveCallId:         state.ActiveCallID,
		PendingOutcomeCallId: state.PendingOutcomeCallID,
	}
}

func callingStateResponse(state humancalling.CallingState) (api.CallingState, error) {
	response := api.CallingState{
		Softphone: softphoneResponse(state.Softphone),
		Ringing:   make([]api.RingingCallLeg, 0, len(state.Ringing)),
	}
	for _, leg := range state.Ringing {
		callID, err := uuid.Parse(leg.CallID)
		if err != nil {
			return api.CallingState{}, err
		}
		callLegID, err := uuid.Parse(leg.CallLegID)
		if err != nil {
			return api.CallingState{}, err
		}
		practiceID, err := uuid.Parse(leg.PracticeID)
		if err != nil {
			return api.CallingState{}, err
		}
		locationID, err := uuid.Parse(leg.LocationID)
		if err != nil {
			return api.CallingState{}, err
		}
		response.Ringing = append(response.Ringing, api.RingingCallLeg{
			CallId: callID, CallLegId: callLegID, PracticeId: practiceID,
			MediaToken: leg.MediaToken,
			LocationId: locationID, LocationName: leg.LocationName,
			DisplayName: leg.DisplayName, Phone: leg.Phone,
			TransferReason: leg.TransferReason,
			State:          api.RingingCallLegState(leg.State), Version: leg.Version,
			CreatedAt: leg.CreatedAt, Deadline: leg.Deadline,
		})
	}
	convert := func(call *humancalling.CallingStateCall) (*api.CallingStateCall, error) {
		if call == nil {
			return nil, nil
		}
		callID, err := uuid.Parse(call.CallID)
		if err != nil {
			return nil, err
		}
		callLegID, err := uuid.Parse(call.CallLegID)
		if err != nil {
			return nil, err
		}
		practiceID, err := uuid.Parse(call.PracticeID)
		if err != nil {
			return nil, err
		}
		locationID, err := uuid.Parse(call.LocationID)
		if err != nil {
			return nil, err
		}
		return &api.CallingStateCall{
			CallId: callID, CallLegId: callLegID, PracticeId: practiceID,
			LocationId: locationID, LocationName: call.LocationName,
			State: call.State, Version: call.Version,
		}, nil
	}
	var err error
	if response.Bridged, err = convert(state.Bridged); err != nil {
		return api.CallingState{}, err
	}
	if response.Voicemail, err = convert(state.Voicemail); err != nil {
		return api.CallingState{}, err
	}
	if response.Disposition, err = convert(state.Disposition); err != nil {
		return api.CallingState{}, err
	}
	return response, nil
}

func callingCallResponse(call humancalling.Call) (api.CallingCall, error) {
	id, err := uuid.Parse(call.ID)
	if err != nil {
		return api.CallingCall{}, err
	}
	practiceID, err := uuid.Parse(call.PracticeID)
	if err != nil {
		return api.CallingCall{}, err
	}
	locationID, err := uuid.Parse(call.LocationID)
	if err != nil {
		return api.CallingCall{}, err
	}
	response := api.CallingCall{
		Id:                  id,
		PracticeId:          practiceID,
		LocationId:          locationID,
		LocationName:        call.LocationName,
		Direction:           api.CallingCallDirection(call.Direction),
		EntryPoint:          api.CallingCallEntryPoint(call.EntryPoint),
		State:               api.CallingCallState(call.State),
		Phone:               call.Phone,
		CallerId:            call.CallerID,
		PhoneSource:         call.PhoneSource,
		DisplayName:         call.DisplayName,
		NameSource:          call.NameSource,
		TransferReason:      call.TransferReason,
		ReasonSource:        call.ReasonSource,
		ProviderTermination: call.ProviderTermination,
		EndRequested:        call.EndRequested,
		RetryAllowed:        call.RetryAllowed,
		Version:             call.Version,
	}
	if call.DispositionDeadline != nil {
		response.DispositionDeadline = call.DispositionDeadline
	}
	if call.RecoveryTask != nil {
		id, err := uuid.Parse(call.RecoveryTask.ID)
		if err != nil {
			return api.CallingCall{}, err
		}
		response.RecoveryTask = &api.CallingRecoveryTask{
			Id:                      id,
			Title:                   call.RecoveryTask.Title,
			State:                   api.CallingRecoveryTaskState(call.RecoveryTask.State),
			RelatedInteractionCount: call.RecoveryTask.RelatedInteractionCount,
		}
	}
	if call.TaskID != "" {
		taskID, err := uuid.Parse(call.TaskID)
		if err != nil {
			return api.CallingCall{}, err
		}
		response.TaskId = &taskID
	}
	if call.RetryOfCallID != "" {
		retryID, err := uuid.Parse(call.RetryOfCallID)
		if err != nil {
			return api.CallingCall{}, err
		}
		response.RetryOfCallId = &retryID
	}
	if call.ConnectedAt != nil {
		response.ConnectedAt = call.ConnectedAt
	}
	if call.Voicemail.Outcome != "" {
		taskID, err := uuid.Parse(call.Voicemail.TaskID)
		if err != nil {
			return api.CallingCall{}, err
		}
		voicemail := api.CallingVoicemail{
			Outcome: api.CallingVoicemailOutcome(
				call.Voicemail.Outcome,
			),
			TaskId:          taskID,
			DurationSeconds: call.Voicemail.DurationSeconds,
		}
		if call.Voicemail.AudioState != "" {
			audioState := api.CallingVoicemailAudioState(
				call.Voicemail.AudioState,
			)
			voicemail.AudioState = &audioState
		}
		response.Voicemail = &voicemail
	}
	if call.Recording.AudioState != "" {
		response.Recording = &api.CallingRecording{
			AudioState:      api.CallingRecordingAudioState(call.Recording.AudioState),
			DurationSeconds: call.Recording.DurationSeconds,
		}
	}
	return response, nil
}

func taskResponse(task work.Task) (api.Task, error) {
	id, err := uuid.Parse(task.ID)
	if err != nil {
		return api.Task{}, err
	}
	practiceID, err := uuid.Parse(task.PracticeID)
	if err != nil {
		return api.Task{}, err
	}
	locationID, err := uuid.Parse(task.LocationID)
	if err != nil {
		return api.Task{}, err
	}
	response := api.Task{
		Id:           id,
		PracticeId:   practiceID,
		LocationId:   locationID,
		LocationName: task.LocationName,
		Phone:        task.Phone,
		Title:        task.Title,
		State:        api.TaskState(task.State),
		Origin:       api.TaskOrigin(task.Origin),
		Urgency:      api.StaffTaskUrgency(task.Urgency),
		CreatedBy: api.TaskActor{
			Kind:    api.TaskActorKind(task.CreatedBy.Kind),
			Subject: task.CreatedBy.Subject,
		},
		CreatedAt:               task.CreatedAt,
		Unread:                  task.Unread,
		Version:                 task.Version,
		UpdatedAt:               task.UpdatedAt,
		RelatedInteractionCount: task.RelatedInteractionCount,
	}
	response.Interactions = make([]api.TaskInteraction, 0, len(task.Interactions))
	for _, interaction := range task.Interactions {
		callID, err := uuid.Parse(interaction.CallID)
		if err != nil {
			return api.Task{}, err
		}
		response.Interactions = append(response.Interactions, api.TaskInteraction{
			CallId: callID, OccurredAt: interaction.OccurredAt,
			Type: api.TaskInteractionType(interaction.Type),
		})
	}
	if task.CallID != "" {
		callID, err := uuid.Parse(task.CallID)
		if err != nil {
			return api.Task{}, err
		}
		response.CallId = &callID
	}
	if task.Category != "" {
		category := api.StaffTaskCategory(task.Category)
		response.Category = &category
	}
	if task.CallerName != "" {
		response.CallerName = &task.CallerName
	}
	if task.SourceCallID != "" {
		response.SourceCallId = &task.SourceCallID
	}
	if task.SourceMessage != "" {
		response.SourceMessage = &task.SourceMessage
	}
	if task.RecoveryOutcome != "" {
		outcome := api.TaskRecoveryOutcome(task.RecoveryOutcome)
		response.RecoveryOutcome = &outcome
	}
	if task.MessageID != "" {
		messageID, err := uuid.Parse(task.MessageID)
		if err != nil {
			return api.Task{}, err
		}
		response.MessageId = &messageID
	}
	if task.MessageThreadID != "" {
		threadID, err := uuid.Parse(task.MessageThreadID)
		if err != nil {
			return api.Task{}, err
		}
		response.MessageThreadId = &threadID
	}
	if task.ConversationThreadID != "" {
		threadID, err := uuid.Parse(task.ConversationThreadID)
		if err != nil {
			return api.Task{}, err
		}
		response.ConversationThreadId = &threadID
	}
	if task.AutomaticAcknowledgement != nil {
		acknowledgement := api.TaskAutomaticAcknowledgement{
			State: api.TaskAutomaticAcknowledgementState(
				task.AutomaticAcknowledgement.State,
			),
			UpdatedAt: task.AutomaticAcknowledgement.UpdatedAt,
		}
		if task.AutomaticAcknowledgement.SafeFailureCode != "" {
			acknowledgement.SafeFailureCode = &task.AutomaticAcknowledgement.SafeFailureCode
		}
		if task.AutomaticAcknowledgement.MessageID != "" {
			messageID, err := uuid.Parse(task.AutomaticAcknowledgement.MessageID)
			if err != nil {
				return api.Task{}, err
			}
			acknowledgement.MessageId = &messageID
		}
		response.AutomaticAcknowledgement = &acknowledgement
	}
	if task.CreatedBy.Email != "" {
		email := apiEmail(task.CreatedBy.Email)
		response.CreatedBy.Email = &email
	}
	if task.CompletedBy != nil {
		response.CompletedBy = &api.TaskActor{
			Kind:    api.TaskActorKind(task.CompletedBy.Kind),
			Subject: task.CompletedBy.Subject,
		}
		if task.CompletedBy.Email != "" {
			email := apiEmail(task.CompletedBy.Email)
			response.CompletedBy.Email = &email
		}
	}
	response.CompletedAt = task.CompletedAt
	return response, nil
}

func messageThreadResponse(thread messaging.Thread) (api.MessageThread, error) {
	id, err := uuid.Parse(thread.ID)
	if err != nil {
		return api.MessageThread{}, err
	}
	practiceID, err := uuid.Parse(thread.PracticeID)
	if err != nil {
		return api.MessageThread{}, err
	}
	locationID, err := uuid.Parse(thread.LocationID)
	if err != nil {
		return api.MessageThread{}, err
	}
	response := api.MessageThread{
		Id:              id,
		PracticeId:      practiceID,
		LocationId:      locationID,
		LocationName:    thread.LocationName,
		OfficePhone:     thread.OfficePhone,
		ExternalPhone:   thread.ExternalPhone,
		OutboundBlocked: thread.OutboundBlocked,
		CreatedAt:       thread.CreatedAt,
		UpdatedAt:       thread.UpdatedAt,
	}
	if thread.DisplayName != "" {
		response.DisplayName = &thread.DisplayName
	}
	if thread.NameSource != "" {
		response.NameSource = &thread.NameSource
	}
	return response, nil
}

func messageThreadPageResponse(
	page messaging.ThreadPage,
) (api.MessageThreadPage, error) {
	response := api.MessageThreadPage{
		Items:      make([]api.MessageThreadSummary, 0, len(page.Items)),
		NextCursor: page.NextCursor,
	}
	for _, item := range page.Items {
		thread, err := messageThreadResponse(item.Thread)
		if err != nil {
			return api.MessageThreadPage{}, err
		}
		summary := api.MessageThreadSummary{
			Id:              thread.Id,
			PracticeId:      thread.PracticeId,
			LocationId:      thread.LocationId,
			LocationName:    thread.LocationName,
			OfficePhone:     thread.OfficePhone,
			ExternalPhone:   thread.ExternalPhone,
			DisplayName:     thread.DisplayName,
			NameSource:      thread.NameSource,
			OutboundBlocked: thread.OutboundBlocked,
			CreatedAt:       thread.CreatedAt,
			UpdatedAt:       thread.UpdatedAt,
			Preview:         item.Preview,
			LatestDirection: api.MessageDirection(item.LatestDirection),
			LatestDelivery:  visibleDelivery(item.LatestDelivery),
			LatestActivity:  item.LatestActivity,
			OpenTaskCount:   item.OpenTaskCount,
			Unread:          item.Unread,
		}
		response.Items = append(response.Items, summary)
	}
	return response, nil
}

func engagementPageResponse(
	page workspace.EngagementPage,
) (api.EngagementPage, error) {
	response := api.EngagementPage{
		Items: make([]api.EngagementSummary, 0, len(page.Items)),
	}
	for _, item := range page.Items {
		summary := api.EngagementSummary{
			Phone:          item.Phone,
			Locations:      make([]api.EngagementLocation, 0, len(item.Locations)),
			LatestActivity: item.LatestActivity,
			OpenTaskCount:  item.OpenTaskCount,
			Unread:         item.Unread,
		}
		if item.DisplayName != "" {
			summary.DisplayName = &item.DisplayName
		}
		for _, itemLocation := range item.Locations {
			locationID, err := uuid.Parse(itemLocation.ID)
			if err != nil {
				return api.EngagementPage{}, err
			}
			summary.Locations = append(summary.Locations, api.EngagementLocation{
				Id:   locationID,
				Name: itemLocation.Name,
			})
		}
		response.Items = append(response.Items, summary)
	}
	return response, nil
}

func messageResponse(message messaging.Message) (api.Message, error) {
	id, err := uuid.Parse(message.ID)
	if err != nil {
		return api.Message{}, err
	}
	thread, err := messageThreadResponse(message.Thread)
	if err != nil {
		return api.Message{}, err
	}
	response := api.Message{
		Id:          id,
		Thread:      thread,
		Direction:   api.MessageDirection(message.Direction),
		Body:        message.Body,
		Sender:      message.Sender,
		Destination: message.Destination,
		Delivery:    visibleDelivery(message.Delivery),
		CreatedAt:   message.CreatedAt,
		UpdatedAt:   message.UpdatedAt,
		Version:     message.Version,
	}
	if message.SafeFailureCode != "" {
		response.SafeFailureCode = &message.SafeFailureCode
	}
	if message.ProviderMessageID != "" {
		response.ProviderMessageId = &message.ProviderMessageID
	}
	if message.TaskID != "" {
		taskID, err := uuid.Parse(message.TaskID)
		if err != nil {
			return api.Message{}, err
		}
		response.TaskId = &taskID
	}
	if message.RetryOfMessageID != "" {
		retryID, err := uuid.Parse(message.RetryOfMessageID)
		if err != nil {
			return api.Message{}, err
		}
		response.RetryOfMessageId = &retryID
	}
	if message.CreatedBy != nil {
		response.CreatedBy = &api.TaskActor{
			Kind:    api.TaskActorKind(message.CreatedBy.Kind),
			Subject: message.CreatedBy.Subject,
		}
	}
	if message.Attachment != nil {
		attachment, err := messageAttachmentResponse(*message.Attachment)
		if err != nil {
			return api.Message{}, err
		}
		response.Attachment = &attachment
	}
	return response, nil
}

func messageAttachmentResponse(
	attachment messaging.Attachment,
) (api.MessageAttachment, error) {
	id, err := uuid.Parse(attachment.ID)
	if err != nil {
		return api.MessageAttachment{}, err
	}
	response := api.MessageAttachment{
		Id:          id,
		Direction:   api.MessageDirection(attachment.Direction),
		State:       visibleAttachmentState(attachment.State),
		FileName:    attachment.FileName,
		ContentType: api.MessageAttachmentContentType(attachment.ContentType),
		ByteSize:    attachment.ByteSize,
		CreatedAt:   attachment.CreatedAt,
		UpdatedAt:   attachment.UpdatedAt,
	}
	if attachment.MessageID != "" {
		messageID, err := uuid.Parse(attachment.MessageID)
		if err != nil {
			return api.MessageAttachment{}, err
		}
		response.MessageId = &messageID
	}
	return response, nil
}

func visibleAttachmentState(
	state messaging.AttachmentState,
) api.MessageAttachmentState {
	switch state {
	case messaging.AttachmentProcessing:
		return api.Processing
	case messaging.AttachmentStored:
		return api.Stored
	case messaging.AttachmentUnavailable:
		return api.AttachmentUnavailable
	default:
		return api.Pending
	}
}

func conversationTimelineResponse(
	page workspace.TimelinePage,
) (api.ConversationTimelinePage, error) {
	response := api.ConversationTimelinePage{
		Items:      make([]api.ConversationTimelineItem, 0, len(page.Items)),
		NextCursor: page.NextCursor,
	}
	for _, item := range page.Items {
		id, err := uuid.Parse(item.ID)
		if err != nil {
			return api.ConversationTimelinePage{}, err
		}
		converted := api.ConversationTimelineItem{
			Id:         id,
			Type:       api.ConversationTimelineItemType(item.Type),
			OccurredAt: item.OccurredAt,
		}
		if item.TaskActivity != "" {
			activity := api.ConversationTimelineItemTaskActivity(item.TaskActivity)
			converted.TaskActivity = &activity
		}
		switch item.Type {
		case "MESSAGE":
			message, err := messageResponse(item.Message)
			if err != nil {
				return api.ConversationTimelinePage{}, err
			}
			converted.Message = &message
		case "TASK":
			task, err := taskResponse(item.Task)
			if err != nil {
				return api.ConversationTimelinePage{}, err
			}
			converted.Task = &task
		case "CALL":
			call, err := callHistoryItemResponse(item.Call)
			if err != nil {
				return api.ConversationTimelinePage{}, err
			}
			converted.Call = &call
		case "AI_INTERACTION":
			aiInteraction, err := aiOutcomeItemResponse(item.AIInteraction)
			if err != nil {
				return api.ConversationTimelinePage{}, err
			}
			converted.AiInteraction = &aiInteraction
		}
		response.Items = append(response.Items, converted)
	}
	return response, nil
}

func visibleDelivery(state messaging.DeliveryState) api.MessageDeliveryState {
	switch state {
	case messaging.DeliverySent:
		return api.MessageDeliveryStateSent
	case messaging.DeliveryDelivered:
		return api.MessageDeliveryStateDelivered
	case messaging.DeliveryFailed:
		return api.MessageDeliveryStateFailed
	case messaging.DeliveryUnknown:
		return api.MessageDeliveryStateStatusUnknown
	default:
		return api.MessageDeliveryStateSending
	}
}

func taskPageResponse(page work.TaskPage) (api.TaskPage, error) {
	response := api.TaskPage{
		Items:      make([]api.Task, 0, len(page.Items)),
		NextCursor: page.NextCursor,
		Counts: api.TaskFolderCounts{
			Tasks:       page.Counts.Tasks,
			MissedCalls: page.Counts.MissedCalls,
			Categories: api.TaskCategoryCounts{
				Billing:       page.Counts.Categories.Billing,
				Appointments:  page.Counts.Categories.Appointments,
				Documentation: page.Counts.Categories.Documentation,
				Optical:       page.Counts.Categories.Optical,
				Medication:    page.Counts.Categories.Medication,
				Referrals:     page.Counts.Categories.Referrals,
				Other:         page.Counts.Categories.Other,
			},
		},
	}
	for _, task := range page.Items {
		item, err := taskResponse(task)
		if err != nil {
			return api.TaskPage{}, err
		}
		response.Items = append(response.Items, item)
	}
	return response, nil
}

func callHistoryResponse(
	history humancalling.CallHistoryPage,
) (api.CallHistoryPage, error) {
	response := api.CallHistoryPage{
		Items:      make([]api.CallHistoryItem, 0, len(history.Items)),
		NextCursor: history.NextCursor,
	}
	for _, item := range history.Items {
		converted, err := callHistoryItemResponse(item)
		if err != nil {
			return api.CallHistoryPage{}, err
		}
		response.Items = append(response.Items, converted)
	}
	return response, nil
}

func callHistoryItemResponse(
	item humancalling.CallHistoryItem,
) (api.CallHistoryItem, error) {
	id, err := uuid.Parse(item.ID)
	if err != nil {
		return api.CallHistoryItem{}, err
	}
	locationID, err := uuid.Parse(item.LocationID)
	if err != nil {
		return api.CallHistoryItem{}, err
	}
	response := api.CallHistoryItem{
		Id:              id,
		Type:            api.CallHistoryItemType(item.Type),
		Direction:       api.CallHistoryItemDirection(item.Direction),
		StartedAt:       item.StartedAt,
		EndedAt:         item.EndedAt,
		DurationSeconds: item.DurationSeconds,
		LocationId:      locationID,
		LocationName:    item.LocationName,
		AnsweredByEmail: item.AnsweredByEmail,
		TransferReason:  item.TransferReason,
		Outcome:         api.CallHistoryItemOutcome(item.Outcome),
		Current:         item.Current,
		Originating:     item.Originating,
	}
	if item.SourceCallID != "" {
		response.SourceCallId = &item.SourceCallID
	}
	return response, nil
}

func operatorTimelineResponse(
	timeline humancalling.OperatorTimeline,
) (api.OperatorCallingTimeline, error) {
	callID, err := uuid.Parse(timeline.CallID)
	if err != nil {
		return api.OperatorCallingTimeline{}, err
	}
	practiceID, err := uuid.Parse(timeline.PracticeID)
	if err != nil {
		return api.OperatorCallingTimeline{}, err
	}
	response := api.OperatorCallingTimeline{
		CallId:     callID,
		PracticeId: practiceID,
		State:      api.OperatorCallingTimelineState(timeline.State),
		Version:    timeline.Version,
		Entries:    make([]api.OperatorCallingTimelineEntry, 0, len(timeline.Entries)),
	}
	for _, entry := range timeline.Entries {
		item := api.OperatorCallingTimelineEntry{
			Kind:            entry.Kind,
			OpaqueReference: entry.OpaqueReference,
			ErrorCode:       entry.ErrorCode,
			CommandAction:   entry.CommandAction,
			CommandState:    entry.CommandState,
			CommandAttempts: entry.CommandAttempts,
			ReceiptState:    entry.ReceiptState,
			AgeSeconds:      entry.AgeSeconds,
			OccurredAt:      entry.OccurredAt,
		}
		if entry.RecoveryReference != "" {
			item.RecoveryReference = &entry.RecoveryReference
		}
		response.Entries = append(response.Entries, item)
	}
	return response, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func rawJSON(value *map[string]interface{}) json.RawMessage {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(*value)
	if err != nil {
		return nil
	}
	return encoded
}

func aiInteractionDetailResponse(
	stored interaction.Interaction,
) (api.AIInteractionDetail, error) {
	id, err := uuid.Parse(stored.ID)
	if err != nil {
		return api.AIInteractionDetail{}, err
	}
	practiceID, err := uuid.Parse(stored.PracticeID)
	if err != nil {
		return api.AIInteractionDetail{}, err
	}
	locationID, err := uuid.Parse(stored.LocationID)
	if err != nil {
		return api.AIInteractionDetail{}, err
	}
	appointment := interaction.ProjectAppointmentDetails(stored)
	response := api.AIInteractionDetail{
		Id:                    id,
		PracticeId:            practiceID,
		LocationId:            locationID,
		LocationName:          stored.LocationName,
		SourceCallId:          stored.SourceCallID,
		Phone:                 stored.Phone,
		OfficePhone:           stored.OfficePhone,
		ExternalPatientId:     stringPointer(stored.ExternalPatientID),
		StartedAt:             stored.StartedAt,
		EndedAt:               stored.EndedAt,
		Status:                api.AIInteractionCallStatus(stored.Status),
		Summary:               stringPointer(stored.Summary),
		AppointmentOutcome:    api.AIAppointmentOutcome(stored.AppointmentOutcome),
		Appointment:           aiAppointmentFactsResponse(appointment.Appointment),
		AppointmentOccurredAt: stored.AppointmentOccurredAt,
		OldAppointmentId:      stringPointer(stored.OldAppointmentID),
		NewAppointmentId:      stringPointer(stored.NewAppointmentID),
		BookingResult:         jsonMap(stored.BookingResult),
		CancellationResult:    jsonMap(stored.CancellationResult),
		CreatedAt:             stored.CreatedAt,
		UpdatedAt:             stored.UpdatedAt,
	}
	if appointment.PreviousAppointment != nil {
		previous := aiAppointmentFactsResponse(*appointment.PreviousAppointment)
		response.PreviousAppointment = &previous
	}
	if stored.AppointmentAction != "" {
		action := api.AIAppointmentAction(stored.AppointmentAction)
		response.AppointmentAction = &action
	}
	return response, nil
}

func aiInteractionEvidenceResponse(
	stored interaction.Interaction,
) (api.AIInteractionEvidence, error) {
	id, err := uuid.Parse(stored.ID)
	if err != nil {
		return api.AIInteractionEvidence{}, err
	}
	return api.AIInteractionEvidence{
		Id:              id,
		Transcript:      jsonMap(stored.Transcript),
		CloseoutPayload: jsonMap(stored.CloseoutPayload),
		CreatedAt:       stored.CreatedAt,
		UpdatedAt:       stored.UpdatedAt,
	}, nil
}

func aiAppointmentFactsResponse(
	facts interaction.AppointmentFacts,
) api.AIAppointmentFacts {
	return api.AIAppointmentFacts{
		AppointmentDate:     stringPointer(facts.AppointmentDate),
		AppointmentId:       stringPointer(facts.AppointmentID),
		AppointmentTime:     stringPointer(facts.AppointmentTime),
		AppointmentTypeName: stringPointer(facts.AppointmentTypeName),
		CareLane:            stringPointer(facts.CareLane),
		LocationName:        stringPointer(facts.LocationName),
		PatientName:         stringPointer(facts.PatientName),
		ProviderName:        stringPointer(facts.ProviderName),
		StartDatetime:       stringPointer(facts.StartDatetime),
	}
}

func aiOutcomePageResponse(
	page interaction.OutcomePage,
) (api.AIOutcomePage, error) {
	response := api.AIOutcomePage{
		Items:      make([]api.AIOutcomeItem, 0, len(page.Items)),
		NextCursor: page.NextCursor,
	}
	if page.Counts != nil {
		response.Counts = &api.AIOutcomeCounts{
			Tasks:         page.Counts.Tasks,
			Bookings:      page.Counts.Bookings,
			Cancellations: page.Counts.Cancellations,
			Reschedules:   page.Counts.Reschedules,
		}
	}
	for _, item := range page.Items {
		converted, err := aiOutcomeItemResponse(item)
		if err != nil {
			return api.AIOutcomePage{}, err
		}
		response.Items = append(response.Items, converted)
	}
	return response, nil
}

func aiOutcomeItemResponse(
	item interaction.OutcomeItem,
) (api.AIOutcomeItem, error) {
	id, err := uuid.Parse(item.ID)
	if err != nil {
		return api.AIOutcomeItem{}, err
	}
	locationID, err := uuid.Parse(item.LocationID)
	if err != nil {
		return api.AIOutcomeItem{}, err
	}
	response := api.AIOutcomeItem{
		Id:                    id,
		LocationId:            locationID,
		LocationName:          item.LocationName,
		SourceCallId:          item.SourceCallID,
		Phone:                 item.Phone,
		ExternalPatientId:     stringPointer(item.ExternalPatientID),
		StartedAt:             item.StartedAt,
		EndedAt:               item.EndedAt,
		Status:                api.AIInteractionCallStatus(item.Status),
		Summary:               stringPointer(item.Summary),
		AppointmentOutcome:    api.AIAppointmentOutcome(item.AppointmentOutcome),
		AppointmentOccurredAt: item.AppointmentOccurredAt,
		OldAppointmentId:      stringPointer(item.OldAppointmentID),
		NewAppointmentId:      stringPointer(item.NewAppointmentID),
	}
	if item.AppointmentAction != "" {
		action := api.AIAppointmentAction(item.AppointmentAction)
		response.AppointmentAction = &action
	}
	return response, nil
}

func operatorAIAnalyticsPageResponse(
	page interaction.AnalyticsPage,
) (api.OperatorAIAnalyticsPage, error) {
	response := api.OperatorAIAnalyticsPage{
		Summary: api.OperatorAIAnalyticsSummary{
			TotalCalls:        page.Summary.TotalCalls,
			BookingCount:      page.Summary.BookingCount,
			CancellationCount: page.Summary.CancellationCount,
			RescheduleCount:   page.Summary.RescheduleCount,
			P50SttMs:          page.Summary.P50SttMs,
			P90SttMs:          page.Summary.P90SttMs,
			P99SttMs:          page.Summary.P99SttMs,
			P50TtftMs:         page.Summary.P50TtftMs,
			P90TtftMs:         page.Summary.P90TtftMs,
			P99TtftMs:         page.Summary.P99TtftMs,
			P50TtsTtfbMs:      page.Summary.P50TtsTtfbMs,
			P90TtsTtfbMs:      page.Summary.P90TtsTtfbMs,
			P99TtsTtfbMs:      page.Summary.P99TtsTtfbMs,
			P50TotalLatencyMs: page.Summary.P50TotalLatencyMs,
			P90TotalLatencyMs: page.Summary.P90TotalLatencyMs,
			P99TotalLatencyMs: page.Summary.P99TotalLatencyMs,
			TransferCount:     page.Summary.TransferCount,
			TransferRate:      page.Summary.TransferRate,
			ToolCallCount:     page.Summary.ToolCallCount,
			ToolErrorCount:    page.Summary.ToolErrorCount,
			ToolFailureRate:   page.Summary.ToolFailureRate,
		},
		Calls:      make([]api.OperatorAICallAnalytics, 0, len(page.Calls)),
		NextCursor: page.NextCursor,
	}
	for _, call := range page.Calls {
		id, err := uuid.Parse(call.ID)
		if err != nil {
			return api.OperatorAIAnalyticsPage{}, err
		}
		locationID, err := uuid.Parse(call.LocationID)
		if err != nil {
			return api.OperatorAIAnalyticsPage{}, err
		}
		response.Calls = append(response.Calls, api.OperatorAICallAnalytics{
			Id:                  id,
			LocationId:          locationID,
			LocationName:        call.LocationName,
			SourceCallId:        call.SourceCallID,
			Phone:               call.Phone,
			StartedAt:           call.StartedAt,
			EndedAt:             call.EndedAt,
			Status:              api.AIInteractionCallStatus(call.Status),
			DurationSeconds:     call.DurationSeconds,
			P50SttMs:            call.P50SttMs,
			P50TtftMs:           call.P50TtftMs,
			P50TtsTtfbMs:        call.P50TtsTtfbMs,
			P50TotalLatencyMs:   call.P50TotalLatencyMs,
			ToolCallCount:       call.ToolCallCount,
			ToolErrorCount:      call.ToolErrorCount,
			ToolActions:         call.ToolActions,
			Transferred:         call.Transferred,
			TranscriptAvailable: call.TranscriptAvailable,
		})
	}
	return response, nil
}

func operatorAIInteractionAnalyticsResponse(
	detail interaction.OperatorAnalyticsDetail,
) (api.OperatorAIInteractionAnalytics, error) {
	base, err := aiInteractionDetailResponse(detail.Interaction)
	if err != nil {
		return api.OperatorAIInteractionAnalytics{}, err
	}
	response := api.OperatorAIInteractionAnalytics{
		Id:                    base.Id,
		PracticeId:            base.PracticeId,
		LocationId:            base.LocationId,
		LocationName:          base.LocationName,
		SourceCallId:          base.SourceCallId,
		Phone:                 base.Phone,
		OfficePhone:           base.OfficePhone,
		ExternalPatientId:     base.ExternalPatientId,
		StartedAt:             base.StartedAt,
		EndedAt:               base.EndedAt,
		Status:                base.Status,
		Summary:               base.Summary,
		AppointmentOutcome:    base.AppointmentOutcome,
		Appointment:           base.Appointment,
		PreviousAppointment:   base.PreviousAppointment,
		AppointmentOccurredAt: base.AppointmentOccurredAt,
		OldAppointmentId:      base.OldAppointmentId,
		NewAppointmentId:      base.NewAppointmentId,
		BookingResult:         base.BookingResult,
		CancellationResult:    base.CancellationResult,
		CreatedAt:             base.CreatedAt,
		UpdatedAt:             base.UpdatedAt,
		P50SttMs:              detail.P50SttMs,
		P50TtftMs:             detail.P50TtftMs,
		P50TtsTtfbMs:          detail.P50TtsTtfbMs,
		P50TotalLatencyMs:     detail.P50TotalLatencyMs,
		Timeline:              make([]api.OperatorAITimelineItem, 0, len(detail.Timeline)),
		ToolExecutions:        make([]api.OperatorAIToolExecution, 0, len(detail.ToolExecutions)),
	}
	for _, item := range detail.Timeline {
		payload := item.Payload
		response.Timeline = append(response.Timeline, api.OperatorAITimelineItem{
			Kind:           api.OperatorAITimelineKind(item.Kind),
			OccurredAt:     item.OccurredAt,
			Text:           stringPointer(item.Text),
			Name:           stringPointer(item.Name),
			CallId:         stringPointer(item.CallID),
			Payload:        mapPointer(payload),
			Error:          stringPointer(item.Error),
			SttMs:          item.SttMs,
			TtftMs:         item.TtftMs,
			TtsTtfbMs:      item.TtsTtfbMs,
			TotalLatencyMs: item.TotalLatencyMs,
		})
	}
	for _, execution := range detail.ToolExecutions {
		response.ToolExecutions = append(response.ToolExecutions, api.OperatorAIToolExecution{
			CallId:        execution.CallID,
			Name:          execution.Name,
			OccurredAt:    execution.OccurredAt,
			Status:        api.OperatorAIToolExecutionStatus(execution.Status),
			OutputClass:   stringPointer(execution.OutputClass),
			DomainOutcome: stringPointer(execution.DomainOutcome),
			DomainStatus:  optionalOperatorAIToolDomainStatus(execution.DomainStatus),
			TaskId:        stringPointer(execution.TaskID),
		})
	}
	return response, nil
}

func optionalOperatorAIToolDomainStatus(value string) *api.OperatorAIToolExecutionDomainStatus {
	if value == "" {
		return nil
	}
	status := api.OperatorAIToolExecutionDomainStatus(value)
	return &status
}

func mapPointer(value map[string]any) *map[string]interface{} {
	if value == nil {
		return nil
	}
	converted := map[string]interface{}(value)
	return &converted
}

func jsonMap(value json.RawMessage) *map[string]interface{} {
	if len(value) == 0 {
		return nil
	}
	decoded := map[string]interface{}{}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if decoder.Decode(&decoded) != nil {
		return nil
	}
	return &decoded
}

func uuidString(value *openapi_types.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func apiEmail(email string) openapi_types.Email {
	return openapi_types.Email(email)
}

var _ IdentityAuthenticator = (*authn.JWKSAuthenticator)(nil)
