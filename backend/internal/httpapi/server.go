package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/authn"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/work"
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
	AllowedOrigin  string
	AcquireTimeout time.Duration
	Observer       observability.Observer
}

type PortalDependencies struct {
	Access               *access.Module
	Authenticator        IdentityAuthenticator
	Calling              *humancalling.Module
	Messaging            *messaging.Module
	Work                 *work.Module
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
	messaging     *messaging.Module
	work          *work.Module
	serviceAuth   ServiceAuthenticator
	observer      observability.Observer
}

type serverDependencies struct {
	access        *access.Module
	authenticator IdentityAuthenticator
	events        EventStreamer
	calling       *humancalling.Module
	messaging     *messaging.Module
	work          *work.Module
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
		dependencies.Work == nil ||
		dependencies.ServiceAuthenticator == nil {
		return nil, fmt.Errorf("portal dependencies are required")
	}
	return newServer("portal-api", config, pool, serverDependencies{
		access:        dependencies.Access,
		authenticator: dependencies.Authenticator,
		calling:       dependencies.Calling,
		messaging:     dependencies.Messaging,
		work:          dependencies.Work,
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
) (http.Handler, error) {
	return NewProviderIngressWithMessaging(config, pool, calling, nil)
}

func NewProviderIngressWithMessaging(
	config Config,
	pool *pgxpool.Pool,
	calling *humancalling.Module,
	messagingModule *messaging.Module,
) (http.Handler, error) {
	if calling == nil {
		return nil, fmt.Errorf("provider-ingress calling module is required")
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

	server := &Server{
		role:          role,
		config:        config,
		pool:          pool,
		access:        dependencies.access,
		authenticator: dependencies.authenticator,
		events:        dependencies.events,
		calling:       dependencies.calling,
		messaging:     dependencies.messaging,
		work:          dependencies.work,
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
	ctx, cancel := server.databaseContext(r)
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
	token := ""
	if body.InvitationToken != nil {
		token = *body.InvitationToken
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	preview, err := server.access.InspectInvitation(ctx, access.InvitationInspection{
		Token: token,
		Email: string(body.Email),
	})
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	response, err := invitationPreviewResponse(preview)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) InspectInvitation(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	var body api.InvitationCredentialRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	preview, err := server.access.InspectInvitation(ctx, access.InvitationInspection{Token: body.Token})
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	response, err := invitationPreviewResponse(preview)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) AcceptInvitation(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.InvitationCredentialRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	authorization, err := server.access.AcceptInvitation(ctx, identity, body.Token)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	response, err := authorizationResponse(authorization)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, response)
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
	ctx, cancel := server.databaseContext(r)
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

func (server *Server) EnterSupportMode(w http.ResponseWriter, r *http.Request) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	var body api.EnterSupportModeRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	support, err := server.access.EnterSupportMode(ctx, access.EnterSupportModeCommand{
		Identity:   identity,
		PracticeID: body.PracticeId.String(),
		Reason:     body.Reason,
		Duration:   time.Duration(body.DurationMinutes) * time.Minute,
	})
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	response, err := supportResponse(support)
	if err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusCreated, response)
}

func (server *Server) RevokeSupportMode(
	w http.ResponseWriter,
	r *http.Request,
	supportSessionID uuid.UUID,
) {
	if !server.portalOnly(w, r) {
		return
	}
	identity, ok := server.authenticate(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	if err := server.access.RevokeSupportMode(ctx, identity, supportSessionID.String()); err != nil {
		server.writeAccessError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	mutation, err := server.access.AddLocation(ctx, access.AddLocationCommand{
		Identity:         identity,
		PracticeID:       practiceID.String(),
		SupportSessionID: body.SupportSessionId.String(),
		Key:              body.Key,
		Name:             body.Name,
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
	if !service.Allows(access.ServiceCapabilityHumanHandoff) ||
		service.PracticeID != body.PracticeId.String() {
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	handoff, err := server.calling.CreateHandoff(ctx, humancalling.CreateHandoffCommand{
		Service:        service,
		LocationID:     body.LocationId.String(),
		SourceCallID:   body.SourceCallId,
		IdempotencyKey: body.IdempotencyKey,
		Contact: humancalling.ContactContext{
			Phone:          body.Contact.Phone,
			PhoneSource:    stringValue(body.Contact.PhoneSource),
			DisplayName:    stringValue(body.Contact.DisplayName),
			NameSource:     stringValue(body.Contact.NameSource),
			TransferReason: stringValue(body.Contact.TransferReason),
			ReasonSource:   stringValue(body.Contact.ReasonSource),
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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

func (server *Server) ListCallingOffers(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	offers, err := server.calling.ListOffers(ctx, identity)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	response := api.CallingOfferList{Items: make([]api.CallingOffer, 0, len(offers))}
	for _, offer := range offers {
		converted, err := callingOfferResponse(offer)
		if err != nil {
			server.writeCallingError(w, r, err)
			return
		}
		response.Items = append(response.Items, converted)
	}
	server.writeJSON(w, http.StatusOK, response)
}

func (server *Server) AcceptCallingOffer(
	w http.ResponseWriter,
	r *http.Request,
	callID openapi_types.UUID,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	var body api.AcceptCallingOfferRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	result, err := server.calling.AcceptOffer(ctx, identity, body.SessionId, callID.String())
	if err != nil {
		outcome := observability.AcceptFailed
		if errors.Is(err, humancalling.ErrDenied) {
			outcome = observability.AcceptDenied
		}
		observability.Record(server.observer, observability.CallAccepted(outcome))
		server.writeCallingError(w, r, err)
		return
	}
	outcome := observability.AcceptFailed
	switch result.Status {
	case humancalling.Accepted:
		outcome = observability.AcceptWon
	case humancalling.AlreadyClaimed:
		outcome = observability.AcceptAlreadyClaimed
	case humancalling.AcceptExpired:
		outcome = observability.AcceptExpired
	case humancalling.AcceptIneligible:
		outcome = observability.AcceptIneligible
	}
	observability.Record(server.observer, observability.CallAccepted(outcome))
	convertedID, err := uuid.Parse(result.CallID)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	server.writeJSON(w, http.StatusOK, api.AcceptCallingOfferResult{
		CallId: convertedID,
		Status: api.AcceptCallingOfferResultStatus(result.Status),
		State:  api.AcceptCallingOfferResultState(result.State),
	})
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	call, err := server.calling.ReadCall(ctx, identity, callID.String())
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	timeline, err := server.messaging.QueryPhoneTimeline(
		ctx,
		messaging.QueryPhoneTimelineCommand{
			Identity:   identity,
			PracticeID: call.PracticeID,
			Phone:      call.Phone,
			Cursor:     stringValue(params.Cursor),
			Limit:      intValue(params.Limit),
		},
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	response, err := conversationTimelineResponse(timeline)
	if err != nil {
		server.writeCallingError(w, r, err)
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	call, err := server.calling.ConfirmOutboundMedia(
		ctx,
		humancalling.ConfirmOutboundMediaCommand{
			Identity:          identity,
			SessionID:         body.SessionId,
			CallID:            callID.String(),
			MediaToken:        body.MediaToken,
			ProviderSessionID: body.ProviderSessionId,
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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

func (server *Server) GetCallingVoicemailPlayback(
	w http.ResponseWriter,
	r *http.Request,
	token string,
) {
	identity, ok := server.callingIdentity(w, r)
	if !ok {
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	content, err := server.calling.OpenVoicemailPlayback(
		ctx,
		identity,
		token,
	)
	if err != nil {
		server.writeCallingError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", content.ContentType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content.Content)
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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
	ordering := work.TaskOrderingTime
	if body.Ordering != nil {
		ordering = work.TaskOrdering(*body.Ordering)
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	page, err := server.work.QueryTasks(ctx, work.QueryTasksCommand{
		Identity:   identity,
		PracticeID: body.PracticeId.String(),
		LocationID: uuidString(body.LocationId),
		Search:     stringValue(body.Search),
		Ordering:   ordering,
		Cursor:     stringValue(body.Cursor),
		Limit:      intValue(body.Limit),
	})
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	if server.messaging != nil {
		if err := server.messaging.ApplyTaskUnread(ctx, identity, page.Items); err != nil {
			server.writeMessagingError(w, r, err)
			return
		}
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	task, err := server.work.ReadTask(ctx, identity, taskID.String())
	if err != nil {
		server.writeWorkError(w, r, err)
		return
	}
	if server.messaging != nil {
		projected := []work.Task{task}
		if err := server.messaging.ApplyTaskUnread(ctx, identity, projected); err != nil {
			server.writeMessagingError(w, r, err)
			return
		}
		task = projected[0]
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	task, err := server.work.RenameTask(ctx, work.RenameTaskCommand{
		Identity:         identity,
		TaskID:           taskID.String(),
		ExpectedVersion:  body.ExpectedVersion,
		Title:            body.Title,
		SupportSessionID: uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	task, err := server.work.CompleteTask(ctx, work.CompleteTaskCommand{
		Identity:         identity,
		TaskID:           taskID.String(),
		ExpectedVersion:  body.ExpectedVersion,
		SupportSessionID: uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	task, err := server.work.ReopenTask(ctx, work.ReopenTaskCommand{
		Identity:         identity,
		TaskID:           taskID.String(),
		ExpectedVersion:  body.ExpectedVersion,
		SupportSessionID: uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	task, err := server.work.ReadTask(ctx, identity, taskID.String())
	if err != nil {
		server.writeWorkError(w, r, err)
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

func (server *Server) QueryMessageThreads(w http.ResponseWriter, r *http.Request) {
	identity, ok := server.messagingIdentity(w, r)
	if !ok {
		return
	}
	var body api.MessageThreadQueryRequest
	if !server.decodeJSON(w, r, &body) {
		return
	}
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	page, err := server.messaging.QueryThreads(
		ctx,
		messaging.QueryThreadsCommand{
			Identity:   identity,
			PracticeID: body.PracticeId.String(),
			LocationID: body.LocationId.String(),
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	page, err := server.messaging.QueryTimeline(
		ctx,
		messaging.QueryTimelineCommand{
			Identity: identity,
			ThreadID: threadID.String(),
			Cursor:   stringValue(params.Cursor),
			Limit:    intValue(params.Limit),
		},
	)
	if err != nil {
		server.writeMessagingError(w, r, err)
		return
	}
	response, err := conversationTimelineResponse(page)
	if err != nil {
		server.writeMessagingError(w, r, err)
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	if err := server.messaging.MarkRead(ctx, messaging.MarkReadCommand{
		Identity:         identity,
		ThreadID:         threadID.String(),
		SupportSessionID: uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	message, status, err := server.messaging.Send(ctx, messaging.SendCommand{
		Identity:         identity,
		PracticeID:       body.PracticeId.String(),
		LocationID:       body.LocationId.String(),
		ThreadID:         uuidString(body.ThreadId),
		Destination:      stringValue(body.Destination),
		Body:             body.Body,
		TaskID:           uuidString(body.TaskId),
		AttachmentID:     uuidString(body.AttachmentId),
		IdempotencyKey:   body.IdempotencyKey,
		SupportSessionID: uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	attachment, err := server.messaging.UploadAttachment(
		ctx,
		messaging.UploadAttachmentCommand{
			Identity:         identity,
			PracticeID:       body.PracticeId.String(),
			LocationID:       body.LocationId.String(),
			FileName:         body.FileName,
			DeclaredType:     string(body.ContentType),
			Content:          content,
			SupportSessionID: uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	attachment, err := server.messaging.RetryAttachment(
		ctx,
		messaging.RetryAttachmentCommand{
			Identity:         identity,
			AttachmentID:     attachmentID.String(),
			SupportSessionID: uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	message, status, err := server.messaging.SendAgain(
		ctx,
		messaging.SendAgainCommand{
			Identity:                  identity,
			MessageID:                 messageID.String(),
			IdempotencyKey:            body.IdempotencyKey,
			DuplicateRiskAcknowledged: body.DuplicateRiskAcknowledged,
			SupportSessionID:          uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	task, status, err := server.messaging.CreateFollowUpTask(
		ctx,
		messaging.CreateFollowUpTaskCommand{
			Identity:         identity,
			MessageID:        messageID.String(),
			Title:            stringValue(body.Title),
			SupportSessionID: uuidString(body.SupportSessionId),
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
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
	ctx, cancel := server.databaseContext(r)
	defer cancel()
	result, err := server.calling.RequeueQuarantinedReceipt(
		ctx,
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         identity,
			PracticeID:       practiceID.String(),
			SupportSessionID: body.SupportSessionId.String(),
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

func (server *Server) databaseContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), server.config.AcquireTimeout)
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
	decoder := json.NewDecoder(io.LimitReader(r.Body, maximumBytes))
	decoder.DisallowUnknownFields()
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
	case errors.Is(err, access.ErrInvitationUsed):
		server.writeError(w, r, http.StatusConflict, "INVITATION_USED", "The invitation has already been accepted.", false)
	case errors.Is(err, access.ErrDenied),
		errors.Is(err, access.ErrEmailNotVerified),
		errors.Is(err, access.ErrInvitationExpired),
		errors.Is(err, access.ErrInvitationRevoked),
		errors.Is(err, access.ErrSupportRequired),
		errors.Is(err, access.ErrSupportExpired),
		errors.Is(err, access.ErrSupportRevoked),
		errors.Is(err, access.ErrSupportPracticeMismatch):
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
	case errors.Is(err, humancalling.ErrDenied),
		errors.Is(err, humancalling.ErrInvalidHandoff),
		errors.Is(err, humancalling.ErrIneligible),
		errors.Is(err, access.ErrSupportRequired),
		errors.Is(err, access.ErrSupportExpired),
		errors.Is(err, access.ErrSupportRevoked),
		errors.Is(err, access.ErrSupportPracticeMismatch):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, humancalling.ErrConflict),
		errors.Is(err, humancalling.ErrExpired),
		errors.Is(err, humancalling.ErrAlreadyClaimed):
		server.writeError(w, r, http.StatusConflict, "CALL_CONFLICT", "The Call state changed. Refresh and try again.", false)
	default:
		server.writeError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "A required dependency is unavailable.", true)
	}
}

func (server *Server) writeWorkError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, work.ErrInvalidInput):
		server.writeError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The request is invalid.", false)
	case errors.Is(err, work.ErrDenied),
		errors.Is(err, access.ErrSupportRequired),
		errors.Is(err, access.ErrSupportExpired),
		errors.Is(err, access.ErrSupportRevoked),
		errors.Is(err, access.ErrSupportPracticeMismatch):
		server.writeError(w, r, http.StatusForbidden, "ACCESS_DENIED", "The requested access is not available.", false)
	case errors.Is(err, work.ErrConflict):
		server.writeError(w, r, http.StatusConflict, "TASK_CONFLICT", "The Task state changed. Refresh and try again.", false)
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
	case errors.Is(err, messaging.ErrDenied),
		errors.Is(err, access.ErrSupportRequired),
		errors.Is(err, access.ErrSupportExpired),
		errors.Is(err, access.ErrSupportRevoked),
		errors.Is(err, access.ErrSupportPracticeMismatch):
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

		if origin := r.Header.Get("Origin"); origin != "" && origin == server.config.AllowedOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Correlation-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

func invitationPreviewResponse(preview access.InvitationPreview) (api.InvitationPreview, error) {
	response := api.InvitationPreview{
		Email:     apiEmail(preview.Email),
		Kind:      api.InvitationPreviewKind(preview.Kind),
		Locations: make([]api.Location, 0, len(preview.Locations)),
	}
	if preview.PracticeID != "" {
		practiceID, err := uuid.Parse(preview.PracticeID)
		if err != nil {
			return api.InvitationPreview{}, err
		}
		response.PracticeId = &practiceID
		response.PracticeName = &preview.PracticeName
		role := api.InvitationPreviewRole(preview.Role)
		scope := api.InvitationPreviewLocationScope(preview.LocationScope)
		response.Role = &role
		response.LocationScope = &scope
		response.ExpiresAt = &preview.ExpiresAt
	}
	for _, location := range preview.Locations {
		converted, err := locationResponse(location)
		if err != nil {
			return api.InvitationPreview{}, err
		}
		response.Locations = append(response.Locations, converted)
	}
	return response, nil
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
			Id:        practiceResponse.Id,
			Name:      practiceResponse.Name,
			Version:   practiceResponse.Version,
			Locations: []api.Location{},
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

func authorizationResponse(authorization access.Authorization) (api.Authorization, error) {
	practice, err := practiceResponse(authorization.Practice)
	if err != nil {
		return api.Authorization{}, err
	}
	response := api.Authorization{
		Actor:            actorResponse(authorization.Actor),
		Practice:         practice,
		Locations:        []api.Location{},
		PlatformOperator: authorization.PlatformOperator,
	}
	if authorization.Membership.ID != "" {
		membership, err := membershipResponse(authorization.Membership)
		if err != nil {
			return api.Authorization{}, err
		}
		response.Membership = &membership
	}
	for _, location := range authorization.Locations {
		converted, err := locationResponse(location)
		if err != nil {
			return api.Authorization{}, err
		}
		response.Locations = append(response.Locations, converted)
	}
	if authorization.ActiveLocation != nil {
		location, err := locationResponse(*authorization.ActiveLocation)
		if err != nil {
			return api.Authorization{}, err
		}
		response.ActiveLocation = &location
	}
	if authorization.SupportMode != nil {
		support, err := supportResponse(*authorization.SupportMode)
		if err != nil {
			return api.Authorization{}, err
		}
		response.SupportMode = &support
	}
	return response, nil
}

func workspaceResponse(authorization access.Authorization) (api.WorkspaceSnapshot, error) {
	if authorization.ActiveLocation == nil {
		return api.WorkspaceSnapshot{}, access.ErrDenied
	}
	converted, err := authorizationResponse(authorization)
	if err != nil {
		return api.WorkspaceSnapshot{}, err
	}
	response := api.WorkspaceSnapshot{
		SchemaVersion:    api.N20260724,
		Version:          authorization.Practice.Version,
		State:            api.EMPTY,
		Actor:            converted.Actor,
		Practice:         converted.Practice,
		Location:         *converted.ActiveLocation,
		Membership:       converted.Membership,
		PlatformOperator: authorization.PlatformOperator,
		SupportMode:      converted.SupportMode,
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

func supportResponse(support access.SupportMode) (api.SupportMode, error) {
	id, err := uuid.Parse(support.ID)
	if err != nil {
		return api.SupportMode{}, err
	}
	practiceID, err := uuid.Parse(support.PracticeID)
	if err != nil {
		return api.SupportMode{}, err
	}
	return api.SupportMode{
		Id:         id,
		PracticeId: practiceID,
		Reason:     support.Reason,
		StartsAt:   support.StartsAt,
		ExpiresAt:  support.ExpiresAt,
	}, nil
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
	supportID, err := uuid.Parse(mutation.Audit.SupportSessionID)
	if err != nil {
		return api.LocationMutation{}, err
	}
	return api.LocationMutation{
		Location:        location,
		PracticeVersion: mutation.PracticeVersion,
		Audit: api.AuditEvent{
			Id:               auditID,
			ActorSubject:     mutation.Audit.ActorSubject,
			PracticeId:       practiceID,
			SupportSessionId: supportID,
			Action:           mutation.Audit.Action,
			Reason:           mutation.Audit.Reason,
			CreatedAt:        mutation.Audit.CreatedAt,
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
		SessionId:      state.SessionID,
		LeaseExpiresAt: state.LeaseExpiresAt,
		Owner:          state.Owner,
		Available:      state.Available,
		ActiveCallId:   state.ActiveCallID,
	}
}

func callingOfferResponse(offer humancalling.Offer) (api.CallingOffer, error) {
	id, err := uuid.Parse(offer.ID)
	if err != nil {
		return api.CallingOffer{}, err
	}
	practiceID, err := uuid.Parse(offer.PracticeID)
	if err != nil {
		return api.CallingOffer{}, err
	}
	locationID, err := uuid.Parse(offer.LocationID)
	if err != nil {
		return api.CallingOffer{}, err
	}
	return api.CallingOffer{
		Id:             id,
		PracticeId:     practiceID,
		LocationId:     locationID,
		LocationName:   offer.LocationName,
		DisplayName:    offer.DisplayName,
		NameSource:     offer.NameSource,
		TransferReason: offer.TransferReason,
		ReasonSource:   offer.ReasonSource,
		Deadline:       offer.Deadline,
		State:          api.CallingOfferState(offer.State),
		Version:        offer.Version,
	}, nil
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
		Deadline:            call.Deadline,
		Phone:               call.Phone,
		CallerId:            call.CallerID,
		PhoneSource:         call.PhoneSource,
		DisplayName:         call.DisplayName,
		NameSource:          call.NameSource,
		TransferReason:      call.TransferReason,
		ReasonSource:        call.ReasonSource,
		ExpectedStaffLegId:  call.ExpectedStaffLegID,
		ExpectedMediaToken:  call.ExpectedMediaToken,
		MediaReady:          call.MediaReady,
		ProviderTermination: call.ProviderTermination,
		RetryAllowed:        call.RetryAllowed,
		Version:             call.Version,
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
	if call.Recording.State != "" {
		recording := api.CallingRecording{
			State: api.CallingRecordingState(call.Recording.State),
		}
		if call.Recording.FailureCode != "" {
			recording.FailureCode = &call.Recording.FailureCode
		}
		response.Recording = &recording
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
		CreatedAt: task.CreatedAt,
		Unread:    task.Unread,
		Version:   task.Version,
		UpdatedAt: task.UpdatedAt,
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
			Unread:          item.Unread,
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
	page messaging.TimelinePage,
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
		}
		response.Items = append(response.Items, converted)
	}
	return response, nil
}

func visibleDelivery(state messaging.DeliveryState) api.MessageDeliveryState {
	switch state {
	case messaging.DeliverySent:
		return api.Sent
	case messaging.DeliveryDelivered:
		return api.Delivered
	case messaging.DeliveryFailed:
		return api.Failed
	case messaging.DeliveryUnknown:
		return api.StatusUnknown
	default:
		return api.Sending
	}
}

func taskPageResponse(page work.TaskPage) (api.TaskPage, error) {
	response := api.TaskPage{
		Items:      make([]api.Task, 0, len(page.Items)),
		NextCursor: page.NextCursor,
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
	return api.CallHistoryItem{
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
	}, nil
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
