package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
)

type Role string

const (
	RoleAdmin Role = "ADMIN"
	RoleStaff Role = "STAFF"
)

type LocationScope string

const (
	LocationScopeAll      LocationScope = "ALL"
	LocationScopeSelected LocationScope = "SELECTED"
)

var (
	ErrDenied             = errors.New("access denied")
	ErrAccessGrantClaimed = errors.New("Access Grant already claimed")
	ErrInvalidInput       = errors.New("invalid access input")
)

var abitaOfficeKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
}

type ActorKind string

const (
	ActorHuman   ActorKind = "HUMAN"
	ActorService ActorKind = "SERVICE"
)

type ServiceCapability string

const (
	ServiceCapabilityHumanHandoff        ServiceCapability = "HUMAN_HANDOFF"
	ServiceCapabilityCreateTask          ServiceCapability = "CREATE_TASK"
	ServiceCapabilityIngestAIInteraction ServiceCapability = "INGEST_AI_INTERACTION"
)

type ServiceIdentity struct {
	Subject       string
	PracticeID    string
	LocationScope LocationScope
	Capabilities  []ServiceCapability
}

func (identity ServiceIdentity) Allows(capability ServiceCapability) bool {
	return serviceHasCapability(identity, capability)
}

type ServiceAuthorization struct {
	Actor      Actor
	PracticeID string
	LocationID string
}

type Provisioning struct {
	Environment             string
	RequestedBy             string
	RequireEmptyAccessState bool
	PlatformOperators       []string
	Practices               []PracticeProvision
}

type PracticeProvision struct {
	Key                                 string
	Name                                string
	ConnectedCallRecordingEnabled       bool
	ConnectedCallRecordingRetentionDays int
	OutboundVoiceFallbackLocationKey    string
	Locations                           []LocationProvision
	AccessGrants                        []AccessGrantProvision
}

type LocationProvision struct {
	Key                string
	Name               string
	AbitaOfficeKeys    []string
	MessagingSender    string
	MessagingProfileID string
	MessagingActive    *bool
	VoiceNumber        string
	VoiceEnabled       *bool
	VoicemailGreeting  string
}

type AccessGrantProvision struct {
	Key                  string
	Email                string
	Role                 Role
	LocationScope        LocationScope
	SelectedLocationKeys []string
}

type Provisioned struct {
	AccessGrantCount int `json:"accessGrantCount"`
}

type Actor struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
	Type    string `json:"type"`
}

type Practice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

type Location struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Membership struct {
	ID            string        `json:"id"`
	Role          Role          `json:"role"`
	LocationScope LocationScope `json:"locationScope"`
}

type Authorization struct {
	Actor            Actor      `json:"actor"`
	Practice         Practice   `json:"practice"`
	Membership       Membership `json:"membership"`
	Locations        []Location `json:"locations"`
	ActiveLocation   *Location  `json:"activeLocation,omitempty"`
	PlatformOperator bool       `json:"platformOperator"`
}

type PracticeAccess struct {
	Practice
	Membership     *Membership `json:"membership,omitempty"`
	Locations      []Location  `json:"locations"`
	CallingEnabled bool        `json:"callingEnabled"`
}

type Discovery struct {
	Actor            Actor            `json:"actor"`
	PlatformOperator bool             `json:"platformOperator"`
	Practices        []PracticeAccess `json:"practices"`
}

// portalCallingEnabled preserves the portal's broad CallingEnabled behavior:
// every Practice member and every Platform Operator may use calling features.
// Inbound fanout is narrower and remains Staff-or-operator only through
// access_calling_scopes.
func portalCallingEnabled(platformOperator bool, membership *Membership) bool {
	return platformOperator || membership != nil
}

type AddLocationCommand struct {
	Identity   Identity
	PracticeID string
	Key        string
	Name       string
}

type RevokeAccessGrantCommand struct {
	Identity      Identity
	PracticeID    string
	AccessGrantID string
}

type RevokeMembershipCommand struct {
	Identity     Identity
	PracticeID   string
	MembershipID string
}

type AuditEvent struct {
	ID           string    `json:"id"`
	ActorSubject string    `json:"actorSubject"`
	PracticeID   string    `json:"practiceId"`
	Action       string    `json:"action"`
	Reason       string    `json:"reason,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}

type OperatorMutationAudit struct {
	Action          string
	ResourceType    string
	ResourceID      string
	ResourceVersion int64
	OccurredAt      time.Time
}

type LocationMutation struct {
	Location        Location   `json:"location"`
	PracticeVersion int64      `json:"practiceVersion"`
	Audit           AuditEvent `json:"audit"`
}

type SignUpEligibilityKind string

const (
	SignUpEligibilityAccessGrant      SignUpEligibilityKind = "ACCESS_GRANT"
	SignUpEligibilityPlatformOperator SignUpEligibilityKind = "PLATFORM_OPERATOR"
)

type SignUpEligibility struct {
	Kind  SignUpEligibilityKind `json:"kind"`
	Email string                `json:"email"`
}

// Module is the Access implementation. Its public methods are the product
// interface; focused PostgreSQL behavior remains local to this package.
type Module struct {
	database productpostgres.Database
	now      func() time.Time
}

func New(database productpostgres.Database, now func() time.Time) *Module {
	if now == nil {
		now = time.Now
	}
	return &Module{database: database, now: now}
}

func (m *Module) Provision(ctx context.Context, input Provisioning) (Provisioned, error) {
	if m.database == nil {
		return Provisioned{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Provisioned{}, fmt.Errorf("begin provisioning: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := m.ProvisionInTx(ctx, tx, input)
	if err != nil {
		return Provisioned{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Provisioned{}, fmt.Errorf("commit provisioning: %w", err)
	}
	return result, nil
}

func (m *Module) ProvisionInTx(
	ctx context.Context,
	tx pgx.Tx,
	input Provisioning,
) (Provisioned, error) {
	if tx == nil {
		return Provisioned{}, ErrInvalidInput
	}
	if err := validateProvisioning(input); err != nil {
		return Provisioned{}, err
	}
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended('acuity.access.provision', 0))
	`); err != nil {
		return Provisioned{}, fmt.Errorf("lock provisioning: %w", err)
	}
	if input.RequireEmptyAccessState {
		var existing bool
		if err := tx.QueryRow(ctx, `
			SELECT
				EXISTS (SELECT 1 FROM access_practices)
				OR EXISTS (SELECT 1 FROM access_platform_operators)
		`).Scan(&existing); err != nil {
			return Provisioned{}, fmt.Errorf("check provisioning target: %w", err)
		}
		if existing {
			return Provisioned{}, fmt.Errorf(
				"%w: provisioning requires empty Access state",
				ErrInvalidInput,
			)
		}
	}

	result := Provisioned{}
	operatorEmails := make([]string, 0, len(input.PlatformOperators))
	for _, email := range input.PlatformOperators {
		operatorEmails = append(operatorEmails, normalizeEmail(email))
	}
	sort.Strings(operatorEmails)
	for _, email := range operatorEmails {
		if err := lockPlatformOperatorEmail(ctx, tx, email); err != nil {
			return Provisioned{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_platform_operators (email)
			VALUES ($1)
			ON CONFLICT (email) DO NOTHING
		`, email); err != nil {
			return Provisioned{}, fmt.Errorf("provision Platform Operator: %w", err)
		}
	}
	for _, practiceInput := range input.Practices {
		retentionDays := practiceInput.ConnectedCallRecordingRetentionDays
		if (practiceInput.ConnectedCallRecordingEnabled && retentionDays == 0) ||
			retentionDays < 0 || retentionDays > 3650 {
			return Provisioned{}, fmt.Errorf(
				"%w: practice %q requires recording retention between 1 and 3650 days when configured",
				ErrInvalidInput,
				practiceInput.Key,
			)
		}
		var practiceID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO access_practices (
				provisioning_key, name, connected_call_recording_enabled,
				connected_call_recording_retention_days
			)
			VALUES ($1, $2, $3, NULLIF($4, 0))
			ON CONFLICT (provisioning_key) DO UPDATE SET
				name = EXCLUDED.name,
				connected_call_recording_enabled = EXCLUDED.connected_call_recording_enabled,
				connected_call_recording_retention_days = EXCLUDED.connected_call_recording_retention_days
			RETURNING id::text
		`, practiceInput.Key, practiceInput.Name,
			practiceInput.ConnectedCallRecordingEnabled,
			practiceInput.ConnectedCallRecordingRetentionDays).Scan(&practiceID); err != nil {
			return Provisioned{}, fmt.Errorf("provision practice %q: %w", practiceInput.Key, err)
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM access_abita_office_locations
			WHERE practice_id = $1
		`, practiceID); err != nil {
			return Provisioned{}, fmt.Errorf(
				"reconcile Abita office routes for practice %q: %w",
				practiceInput.Key,
				err,
			)
		}

		locationIDs := make(map[string]string, len(practiceInput.Locations))
		for _, locationInput := range practiceInput.Locations {
			var locationID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO access_locations (practice_id, provisioning_key, name)
				VALUES ($1, $2, $3)
				ON CONFLICT (practice_id, provisioning_key)
				DO UPDATE SET name = EXCLUDED.name
				RETURNING id::text
			`, practiceID, locationInput.Key, locationInput.Name).Scan(&locationID); err != nil {
				return Provisioned{}, fmt.Errorf("provision location %q: %w", locationInput.Key, err)
			}
			locationIDs[locationInput.Key] = locationID
			for _, officeKey := range locationInput.AbitaOfficeKeys {
				if _, err := tx.Exec(ctx, `
					INSERT INTO access_abita_office_locations (
						practice_id,
						office_key,
						location_id
					)
					VALUES ($1, $2, $3)
					ON CONFLICT (practice_id, office_key)
					DO UPDATE SET location_id = EXCLUDED.location_id
				`, practiceID, officeKey, locationID); err != nil {
					return Provisioned{}, fmt.Errorf(
						"provision Abita office route %q: %w",
						officeKey,
						err,
					)
				}
			}
		}

		for _, grantInput := range practiceInput.AccessGrants {
			var grantID, email string
			var role Role
			var scope LocationScope
			err := tx.QueryRow(ctx, `
				SELECT id::text, email, role, location_scope
				FROM access_grants
				WHERE practice_id = $1 AND provisioning_key = $2
				FOR UPDATE
			`, practiceID, grantInput.Key).Scan(&grantID, &email, &role, &scope)
			if err == nil {
				matches, err := accessGrantMatchesProvisioning(
					ctx, tx, grantID, email, role, scope, grantInput,
				)
				if err != nil {
					return Provisioned{}, err
				}
				if !matches {
					return Provisioned{}, fmt.Errorf(
						"%w: existing Access Grant %q differs from provisioning input",
						ErrInvalidInput,
						grantInput.Key,
					)
				}
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return Provisioned{}, fmt.Errorf("load Access Grant %q: %w", grantInput.Key, err)
			}
			if err := tx.QueryRow(ctx, `
				INSERT INTO access_grants (
					provisioning_key, practice_id, email, role, location_scope
				)
				VALUES ($1, $2, $3, $4, $5)
				RETURNING id::text
			`,
				grantInput.Key,
				practiceID,
				normalizeEmail(grantInput.Email),
				grantInput.Role,
				grantInput.LocationScope,
			).Scan(&grantID); err != nil {
				return Provisioned{}, fmt.Errorf("create Access Grant %q: %w", grantInput.Key, err)
			}
			result.AccessGrantCount++
			for _, locationKey := range grantInput.SelectedLocationKeys {
				locationID, ok := locationIDs[locationKey]
				if !ok {
					return Provisioned{}, fmt.Errorf("%w: Access Grant %q references Location %q", ErrInvalidInput, grantInput.Key, locationKey)
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO access_grant_locations (
						access_grant_id, location_id, practice_id
					)
					VALUES ($1, $2, $3)
				`, grantID, locationID, practiceID); err != nil {
					return Provisioned{}, fmt.Errorf("grant Access Grant Location %q: %w", locationKey, err)
				}
			}
		}

		details, _ := json.Marshal(map[string]any{
			"practiceKey":  practiceInput.Key,
			"locations":    len(practiceInput.Locations),
			"accessGrants": len(practiceInput.AccessGrants),
		})
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_audit_events (
				actor_type, actor_subject, practice_id, action, details
			)
			VALUES ('PROVISIONER', $1, $2, 'access.provisioned', $3)
		`, input.RequestedBy, practiceID, details); err != nil {
			return Provisioned{}, fmt.Errorf("audit provisioning: %w", err)
		}
	}

	return result, nil
}

func accessGrantMatchesProvisioning(
	ctx context.Context,
	tx pgx.Tx,
	grantID string,
	email string,
	role Role,
	scope LocationScope,
	input AccessGrantProvision,
) (bool, error) {
	if email != normalizeEmail(input.Email) || role != input.Role || scope != input.LocationScope {
		return false, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT location.provisioning_key
		FROM access_grant_locations allowed
		JOIN access_locations location
			ON location.practice_id = allowed.practice_id
			AND location.id = allowed.location_id
		WHERE allowed.access_grant_id = $1
		ORDER BY location.provisioning_key
	`, grantID)
	if err != nil {
		return false, fmt.Errorf("load Access Grant Locations: %w", err)
	}
	defer rows.Close()
	actual := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return false, fmt.Errorf("scan Access Grant Location: %w", err)
		}
		actual = append(actual, key)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate Access Grant Locations: %w", err)
	}
	expected := append([]string(nil), input.SelectedLocationKeys...)
	sort.Strings(expected)
	if len(actual) != len(expected) {
		return false, nil
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false, nil
		}
	}
	return true, nil
}

func (m *Module) InspectSignUpEligibility(
	ctx context.Context,
	emailInput string,
) (SignUpEligibility, error) {
	email := normalizeEmail(emailInput)
	if email == "" {
		return SignUpEligibility{}, ErrDenied
	}
	var grantExists bool
	if err := m.database.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM access_grants
			WHERE email = $1
				AND revoked_at IS NULL
				AND claimed_at IS NULL
		)
	`, email).Scan(&grantExists); err != nil {
		return SignUpEligibility{}, fmt.Errorf("inspect Access Grant eligibility: %w", err)
	}
	if grantExists {
		return SignUpEligibility{Kind: SignUpEligibilityAccessGrant, Email: email}, nil
	}
	var operatorExists bool
	if err := m.database.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM access_platform_operators
			WHERE email = $1
		)
	`, email).Scan(&operatorExists); err != nil {
		return SignUpEligibility{}, fmt.Errorf("inspect Platform Operator eligibility: %w", err)
	}
	if !operatorExists {
		return SignUpEligibility{}, ErrDenied
	}
	return SignUpEligibility{
		Kind:  SignUpEligibilityPlatformOperator,
		Email: email,
	}, nil
}

// ResolveActor returns current authorization from PostgreSQL. JWT claims never
// carry Practice, role, or Location authority.
func (m *Module) ResolveActor(
	ctx context.Context,
	identity Identity,
	practiceID string,
	locationID string,
) (Authorization, error) {
	if !identity.EmailVerified || strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(practiceID) == "" {
		return Authorization{}, ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Authorization{}, fmt.Errorf("begin actor resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return Authorization{}, err
	}
	if isOperator {
		authorized, err := loadOperatorAuthorization(ctx, tx, identity, practiceID)
		if err != nil {
			return Authorization{}, err
		}
		if err := selectRequestedLocation(&authorized, locationID); err != nil {
			return Authorization{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Authorization{}, fmt.Errorf("commit operator resolution: %w", err)
		}
		return authorized, nil
	}

	authorized, err := loadMembershipAuthorization(ctx, tx, identity, practiceID, false)
	if err != nil {
		return Authorization{}, err
	}
	if err := selectRequestedLocation(&authorized, locationID); err != nil {
		return Authorization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Authorization{}, fmt.Errorf("commit actor resolution: %w", err)
	}
	return authorized, nil
}

// LockMembershipAuthorization resolves current operational authority inside a
// caller-owned transaction and locks the Membership against concurrent
// revocation until that transaction commits.
func (m *Module) LockMembershipAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	practiceID string,
	locationID string,
) (Authorization, error) {
	return m.lockAuthorization(ctx, tx, identity, practiceID, locationID, true)
}

// LockMutationAuthorization resolves current customer-data mutation authority
// inside a caller-owned transaction. Practice Users use their current
// Membership; Platform Operators write directly under their own identity.
func (m *Module) LockMutationAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	practiceID string,
	locationID string,
) (Authorization, error) {
	return m.lockAuthorization(ctx, tx, identity, practiceID, locationID, true)
}

// LockReadAuthorization resolves current read authority inside a caller-owned
// transaction so revocation cannot race the protected query.
func (m *Module) LockReadAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	practiceID string,
	locationID string,
) (Authorization, error) {
	return m.lockAuthorization(ctx, tx, identity, practiceID, locationID, false)
}

func (m *Module) lockAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	practiceID string,
	locationID string,
	requireLocation bool,
) (Authorization, error) {
	if tx == nil ||
		!identity.EmailVerified ||
		strings.TrimSpace(identity.Subject) == "" ||
		strings.TrimSpace(practiceID) == "" ||
		(requireLocation && strings.TrimSpace(locationID) == "") {
		return Authorization{}, ErrDenied
	}
	_, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return Authorization{}, err
	}
	if isOperator {
		authorization, err := loadOperatorAuthorization(
			ctx,
			tx,
			identity,
			practiceID,
		)
		if err != nil {
			return Authorization{}, err
		}
		if err := selectRequestedLocation(&authorization, locationID); err != nil {
			return Authorization{}, err
		}
		return authorization, nil
	}

	authorization, err := loadMembershipAuthorization(ctx, tx, identity, practiceID, true)
	if err != nil {
		return Authorization{}, err
	}
	if err := selectRequestedLocation(&authorization, locationID); err != nil {
		return Authorization{}, err
	}
	return authorization, nil
}

// LockServiceAuthorization binds an authenticated service capability and Abita
// office key to a current Location inside the caller's transaction.
func (m *Module) LockServiceAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity ServiceIdentity,
	officeKey string,
	capability ServiceCapability,
) (ServiceAuthorization, error) {
	officeKey = strings.TrimSpace(officeKey)
	if tx == nil ||
		strings.TrimSpace(identity.Subject) == "" ||
		strings.TrimSpace(identity.PracticeID) == "" ||
		identity.LocationScope != LocationScopeAll ||
		officeKey == "" ||
		!identity.Allows(capability) {
		return ServiceAuthorization{}, ErrDenied
	}
	var locationID string
	if err := tx.QueryRow(ctx, `
		SELECT route.location_id::text
		FROM access_abita_office_locations route
		JOIN access_locations location
			ON location.practice_id = route.practice_id
			AND location.id = route.location_id
		WHERE route.practice_id = $1
			AND route.office_key = $2
		FOR SHARE OF location
	`, identity.PracticeID, officeKey).Scan(&locationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ServiceAuthorization{}, ErrDenied
		}
		return ServiceAuthorization{}, fmt.Errorf(
			"lock service Location authorization: %w",
			err,
		)
	}
	return ServiceAuthorization{
		Actor: Actor{
			Subject: identity.Subject,
			Type:    string(ActorService),
		},
		PracticeID: identity.PracticeID,
		LocationID: locationID,
	}, nil
}

// LockServiceVoiceAuthorization binds an authenticated service capability and
// one enabled Product voice number to its current Location.
func (m *Module) LockServiceVoiceAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity ServiceIdentity,
	phone string,
	capability ServiceCapability,
) (ServiceAuthorization, error) {
	phone = strings.TrimSpace(phone)
	if tx == nil ||
		strings.TrimSpace(identity.Subject) == "" ||
		strings.TrimSpace(identity.PracticeID) == "" ||
		identity.LocationScope != LocationScopeAll ||
		phone == "" ||
		!identity.Allows(capability) {
		return ServiceAuthorization{}, ErrDenied
	}
	var locationID string
	if err := tx.QueryRow(ctx, `
		SELECT voice.location_id::text
		FROM human_calling_location_voice_numbers voice
		JOIN access_locations location
			ON location.practice_id = voice.practice_id
			AND location.id = voice.location_id
		WHERE voice.practice_id = $1
			AND voice.phone = $2
			AND voice.enabled
			AND NOT EXISTS (
				SELECT 1
				FROM human_calling_location_voice_numbers duplicate
				WHERE duplicate.practice_id = voice.practice_id
					AND duplicate.phone = voice.phone
					AND duplicate.enabled
					AND duplicate.location_id <> voice.location_id
			)
		FOR SHARE OF location
	`, identity.PracticeID, phone).Scan(&locationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ServiceAuthorization{}, ErrDenied
		}
		return ServiceAuthorization{}, fmt.Errorf(
			"lock service voice Location authorization: %w",
			err,
		)
	}
	return ServiceAuthorization{
		Actor: Actor{
			Subject: identity.Subject,
			Type:    string(ActorService),
		},
		PracticeID: identity.PracticeID,
		LocationID: locationID,
	}, nil
}

// LockOperationalActor holds the actor's current authority against concurrent
// changes and returns every Practice in which they may operate.
func (m *Module) LockOperationalActor(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
) ([]string, error) {
	if tx == nil ||
		!identity.EmailVerified ||
		strings.TrimSpace(identity.Subject) == "" {
		return nil, ErrDenied
	}
	_, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return nil, err
	}
	if isOperator {
		rows, err := tx.Query(ctx, `
			SELECT id::text
			FROM access_practices
			ORDER BY id
			FOR SHARE
		`)
		if err != nil {
			return nil, fmt.Errorf("lock operator Practices: %w", err)
		}
		defer rows.Close()
		practiceIDs := []string{}
		for rows.Next() {
			var practiceID string
			if err := rows.Scan(&practiceID); err != nil {
				return nil, fmt.Errorf("scan operator Practice: %w", err)
			}
			practiceIDs = append(practiceIDs, practiceID)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate operator Practices: %w", err)
		}
		if len(practiceIDs) == 0 {
			return nil, ErrDenied
		}
		return practiceIDs, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT practice_id::text
		FROM access_memberships
		WHERE user_subject = $1
			AND revoked_at IS NULL
		ORDER BY practice_id
		FOR SHARE
	`, identity.Subject)
	if err != nil {
		return nil, fmt.Errorf("lock operational actor: %w", err)
	}
	defer rows.Close()
	practiceIDs := []string{}
	for rows.Next() {
		var practiceID string
		if err := rows.Scan(&practiceID); err != nil {
			return nil, fmt.Errorf("scan operational Practice: %w", err)
		}
		practiceIDs = append(practiceIDs, practiceID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operational Practices: %w", err)
	}
	if len(practiceIDs) == 0 {
		return nil, ErrDenied
	}
	return practiceIDs, nil
}

// DiscoverActor returns every currently authorized Practice and Location.
func (m *Module) DiscoverActor(ctx context.Context, identity Identity) (Discovery, error) {
	if !identity.EmailVerified || strings.TrimSpace(identity.Subject) == "" {
		return Discovery{}, ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Discovery{}, fmt.Errorf("begin actor discovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return Discovery{}, err
	}
	auditActorType := "HUMAN"
	if isOperator {
		auditActorType = "PLATFORM_OPERATOR"
	}
	if !isOperator {
		if err := m.activateProvisionedEmail(ctx, tx, identity, m.now(), auditActorType); err != nil {
			return Discovery{}, err
		}
	}
	if !isOperator {
		rows, err := tx.Query(ctx, `
			SELECT
				membership.id::text,
				membership.role,
				membership.location_scope,
				practice.id::text,
				practice.name,
				practice.workspace_version,
				COALESCE(location.id::text, ''),
				COALESCE(location.name, '')
			FROM access_memberships membership
			JOIN access_practices practice
				ON practice.id = membership.practice_id
			LEFT JOIN access_locations location
				ON location.practice_id = membership.practice_id
				AND (
					membership.location_scope = 'ALL'
					OR EXISTS (
						SELECT 1
						FROM access_membership_locations allowed
						WHERE allowed.membership_id = membership.id
							AND allowed.practice_id = membership.practice_id
							AND allowed.location_id = location.id
					)
				)
			WHERE membership.user_subject = $1
				AND membership.revoked_at IS NULL
			ORDER BY practice.name, practice.id, location.name, location.id
		`, identity.Subject)
		if err != nil {
			return Discovery{}, fmt.Errorf("discover Memberships: %w", err)
		}
		result := Discovery{
			Actor: Actor{
				Subject: identity.Subject,
				Email:   normalizeEmail(identity.Email),
				Type:    "HUMAN",
			},
			Practices: []PracticeAccess{},
		}
		for rows.Next() {
			var membership Membership
			var practice Practice
			var location Location
			if err := rows.Scan(
				&membership.ID,
				&membership.Role,
				&membership.LocationScope,
				&practice.ID,
				&practice.Name,
				&practice.Version,
				&location.ID,
				&location.Name,
			); err != nil {
				rows.Close()
				return Discovery{}, fmt.Errorf("scan discovered Membership: %w", err)
			}
			if len(result.Practices) == 0 ||
				result.Practices[len(result.Practices)-1].ID != practice.ID {
				result.Practices = append(result.Practices, PracticeAccess{
					Practice:       practice,
					Membership:     &membership,
					Locations:      []Location{},
					CallingEnabled: portalCallingEnabled(false, &membership),
				})
			}
			if location.ID != "" {
				index := len(result.Practices) - 1
				result.Practices[index].Locations = append(
					result.Practices[index].Locations,
					location,
				)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Discovery{}, fmt.Errorf("iterate discovered Memberships: %w", err)
		}
		rows.Close()
		if len(result.Practices) == 0 {
			return Discovery{}, ErrDenied
		}
		if err := tx.Commit(ctx); err != nil {
			return Discovery{}, fmt.Errorf("commit member discovery: %w", err)
		}
		return result, nil
	}
	result := Discovery{
		Actor: Actor{
			Subject: identity.Subject,
			Email:   normalizeEmail(identity.Email),
			Type:    "PLATFORM_OPERATOR",
		},
		PlatformOperator: true,
		Practices:        []PracticeAccess{},
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, workspace_version
		FROM access_practices
		ORDER BY name, id
	`)
	if err != nil {
		return Discovery{}, fmt.Errorf("discover Practices: %w", err)
	}
	for rows.Next() {
		var practice PracticeAccess
		if err := rows.Scan(&practice.ID, &practice.Name, &practice.Version); err != nil {
			rows.Close()
			return Discovery{}, fmt.Errorf("scan discovered Practice: %w", err)
		}
		practice.CallingEnabled = portalCallingEnabled(true, nil)
		practice.Locations = []Location{}
		result.Practices = append(result.Practices, practice)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Discovery{}, fmt.Errorf("iterate discovered Practices: %w", err)
	}
	rows.Close()
	practiceIDs := make([]string, 0, len(result.Practices))
	practiceIndexes := make(map[string]int, len(result.Practices))
	for index := range result.Practices {
		practiceID := result.Practices[index].ID
		practiceIDs = append(practiceIDs, practiceID)
		practiceIndexes[practiceID] = index
	}
	rows, err = tx.Query(ctx, `
		SELECT practice_id::text, id::text, name
		FROM access_locations
		WHERE practice_id = ANY($1::uuid[])
		ORDER BY practice_id, name, id
	`, practiceIDs)
	if err != nil {
		return Discovery{}, fmt.Errorf("discover Practice Locations: %w", err)
	}
	for rows.Next() {
		var practiceID string
		var location Location
		if err := rows.Scan(&practiceID, &location.ID, &location.Name); err != nil {
			rows.Close()
			return Discovery{}, fmt.Errorf("scan discovered Practice Location: %w", err)
		}
		index, ok := practiceIndexes[practiceID]
		if !ok {
			rows.Close()
			return Discovery{}, ErrDenied
		}
		result.Practices[index].Locations = append(result.Practices[index].Locations, location)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Discovery{}, fmt.Errorf("iterate discovered Practice Locations: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return Discovery{}, fmt.Errorf("commit actor discovery: %w", err)
	}
	return result, nil
}

func (m *Module) activateProvisionedEmail(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	now time.Time,
	auditActorType string,
) error {
	type pendingAccess struct {
		grantID    string
		practiceID string
		role       Role
		scope      LocationScope
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, practice_id::text, role, location_scope
		FROM access_grants
		WHERE email = $1
			AND revoked_at IS NULL
			AND claimed_at IS NULL
		ORDER BY created_at, id
		FOR UPDATE
	`, normalizeEmail(identity.Email))
	if err != nil {
		return fmt.Errorf("load provisioned email access: %w", err)
	}
	pending := []pendingAccess{}
	for rows.Next() {
		var item pendingAccess
		if err := rows.Scan(
			&item.grantID,
			&item.practiceID,
			&item.role,
			&item.scope,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan provisioned email access: %w", err)
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate provisioned email access: %w", err)
	}
	rows.Close()

	for _, item := range pending {
		var membershipID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO access_memberships (
				user_subject, email, practice_id, role, location_scope, access_grant_id
			)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id::text
		`,
			identity.Subject,
			normalizeEmail(identity.Email),
			item.practiceID,
			item.role,
			item.scope,
			item.grantID,
		).Scan(&membershipID); err != nil {
			return fmt.Errorf("activate provisioned email access: %w", err)
		}
		if item.scope == LocationScopeSelected {
			if _, err := tx.Exec(ctx, `
				INSERT INTO access_membership_locations (membership_id, location_id, practice_id)
				SELECT $1, location_id, practice_id
				FROM access_grant_locations
				WHERE access_grant_id = $2
			`, membershipID, item.grantID); err != nil {
				return fmt.Errorf("activate provisioned Location access: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE access_grants
			SET claimed_at = $2, claimed_by_subject = $3
			WHERE id = $1
		`, item.grantID, now, identity.Subject); err != nil {
			return fmt.Errorf("claim provisioned email access: %w", err)
		}
		details, _ := json.Marshal(map[string]any{
			"accessGrantId": item.grantID,
			"membershipId":  membershipID,
		})
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_audit_events (
				actor_type, actor_subject, practice_id, action, details
			)
			VALUES ($1, $2, $3, 'access_grant.claimed', $4)
		`, auditActorType, identity.Subject, item.practiceID, details); err != nil {
			return fmt.Errorf("audit provisioned email access: %w", err)
		}
		if _, err := m.RecordWorkspaceChange(ctx, tx, item.practiceID); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) RevokeAccessGrant(
	ctx context.Context,
	command RevokeAccessGrantCommand,
) error {
	if !command.Identity.EmailVerified || command.Identity.Subject == "" ||
		command.PracticeID == "" || command.AccessGrantID == "" {
		return ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Access Grant revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, isOperator, err := bindPlatformOperator(ctx, tx, command.Identity); err != nil {
		return err
	} else if !isOperator {
		return ErrDenied
	}

	var claimedAt, revokedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT claimed_at, revoked_at
		FROM access_grants
		WHERE id = $1 AND practice_id = $2
		FOR UPDATE
	`, command.AccessGrantID, command.PracticeID).Scan(&claimedAt, &revokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDenied
		}
		return fmt.Errorf("load Access Grant for revocation: %w", err)
	}
	if claimedAt != nil {
		return ErrAccessGrantClaimed
	}
	if revokedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit repeated Access Grant revocation: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE access_grants
		SET revoked_at = $2
		WHERE id = $1
	`, command.AccessGrantID, m.now()); err != nil {
		return fmt.Errorf("revoke Access Grant: %w", err)
	}
	if err := auditRevocation(
		ctx,
		tx,
		command.Identity.Subject,
		command.PracticeID,
		"access_grant.revoked",
		"accessGrantId",
		command.AccessGrantID,
	); err != nil {
		return err
	}
	if _, err := m.RecordWorkspaceChange(ctx, tx, command.PracticeID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Access Grant revocation: %w", err)
	}
	return nil
}

func (m *Module) RevokeMembership(
	ctx context.Context,
	command RevokeMembershipCommand,
) error {
	if !command.Identity.EmailVerified || command.Identity.Subject == "" ||
		command.PracticeID == "" || command.MembershipID == "" {
		return ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Membership revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, isOperator, err := bindPlatformOperator(ctx, tx, command.Identity); err != nil {
		return err
	} else if !isOperator {
		return ErrDenied
	}

	var revokedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT revoked_at
		FROM access_memberships
		WHERE id = $1 AND practice_id = $2
		FOR UPDATE
	`, command.MembershipID, command.PracticeID).Scan(&revokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDenied
		}
		return fmt.Errorf("load Membership for revocation: %w", err)
	}
	if revokedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit repeated Membership revocation: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE access_memberships
		SET revoked_at = $2
		WHERE id = $1
	`, command.MembershipID, m.now()); err != nil {
		return fmt.Errorf("revoke Membership: %w", err)
	}
	if err := auditRevocation(
		ctx,
		tx,
		command.Identity.Subject,
		command.PracticeID,
		"membership.revoked",
		"membershipId",
		command.MembershipID,
	); err != nil {
		return err
	}
	if _, err := m.RecordWorkspaceChange(ctx, tx, command.PracticeID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Membership revocation: %w", err)
	}
	return nil
}

// AuditTrail returns the Practice-scoped immutable audit view for a verified
// Platform Operator. It never substitutes the target customer as actor.
func (m *Module) AuditTrail(
	ctx context.Context,
	identity Identity,
	practiceID string,
) ([]AuditEvent, error) {
	if !identity.EmailVerified || identity.Subject == "" || practiceID == "" {
		return nil, ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin audit trail: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return nil, err
	}
	if !isOperator {
		return nil, ErrDenied
	}
	rows, err := tx.Query(ctx, `
		SELECT
			id::text,
			actor_subject,
			practice_id::text,
			action,
			COALESCE(reason, ''),
			created_at
		FROM access_audit_events
		WHERE practice_id = $1
		ORDER BY created_at, id
	`, practiceID)
	if err != nil {
		return nil, fmt.Errorf("load audit trail: %w", err)
	}
	defer rows.Close()
	events := []AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(
			&event.ID,
			&event.ActorSubject,
			&event.PracticeID,
			&event.Action,
			&event.Reason,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit trail: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit audit trail: %w", err)
	}
	return events, nil
}

func (m *Module) AddLocation(
	ctx context.Context,
	command AddLocationCommand,
) (LocationMutation, error) {
	if !command.Identity.EmailVerified || command.Identity.Subject == "" ||
		command.PracticeID == "" || strings.TrimSpace(command.Key) == "" ||
		strings.TrimSpace(command.Name) == "" {
		return LocationMutation{}, ErrDenied
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return LocationMutation{}, fmt.Errorf("begin Location mutation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, isOperator, err := bindPlatformOperator(ctx, tx, command.Identity); err != nil {
		return LocationMutation{}, err
	} else if !isOperator {
		return LocationMutation{}, ErrDenied
	}

	var result LocationMutation
	if err := tx.QueryRow(ctx, `
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (practice_id, provisioning_key)
		DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text, name
	`, command.PracticeID, command.Key, strings.TrimSpace(command.Name)).Scan(
		&result.Location.ID,
		&result.Location.Name,
	); err != nil {
		return LocationMutation{}, fmt.Errorf("add Location: %w", err)
	}
	result.Audit = AuditEvent{
		ActorSubject: command.Identity.Subject,
		PracticeID:   command.PracticeID,
		Action:       "location.added",
		CreatedAt:    m.now(),
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO access_audit_events (
			actor_type, actor_subject, practice_id, action, details, created_at
		)
		VALUES (
			'PLATFORM_OPERATOR', $1, $2, $3,
			jsonb_build_object('locationId', $4::text), $5
		)
		RETURNING id::text
	`,
		result.Audit.ActorSubject,
		result.Audit.PracticeID,
		result.Audit.Action,
		result.Location.ID,
		result.Audit.CreatedAt,
	).Scan(&result.Audit.ID); err != nil {
		return LocationMutation{}, fmt.Errorf("audit Location mutation: %w", err)
	}
	result.PracticeVersion, err = m.RecordWorkspaceChange(ctx, tx, command.PracticeID)
	if err != nil {
		return LocationMutation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LocationMutation{}, fmt.Errorf("commit Location mutation: %w", err)
	}
	return result, nil
}

func auditRevocation(
	ctx context.Context,
	tx pgx.Tx,
	actorSubject string,
	practiceID string,
	action string,
	targetKey string,
	targetID string,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_audit_events (
			actor_type, actor_subject, practice_id, action, details
		)
		VALUES (
			'PLATFORM_OPERATOR', $1, $2, $3,
			jsonb_build_object($4::text, $5::text)
		)
	`, actorSubject, practiceID, action, targetKey, targetID); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

func bindPlatformOperator(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
) (string, bool, error) {
	email := normalizeEmail(identity.Email)
	var operatorID string
	err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM access_platform_operators
		WHERE user_subject = $1 AND email = $2
	`, identity.Subject, email).Scan(&operatorID)
	if err == nil {
		return operatorID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("resolve bound Platform Operator: %w", err)
	}

	if err := lockPlatformOperatorIdentity(ctx, tx, identity); err != nil {
		return "", false, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, email, COALESCE(user_subject, '')
		FROM access_platform_operators
		WHERE user_subject = $1 OR email = $2
		ORDER BY id
		FOR UPDATE
	`, identity.Subject, email)
	if err != nil {
		return "", false, fmt.Errorf("resolve Platform Operator: %w", err)
	}
	defer rows.Close()
	var emailOperatorID string
	for rows.Next() {
		var id, rowEmail, subject string
		if err := rows.Scan(&id, &rowEmail, &subject); err != nil {
			return "", false, fmt.Errorf("scan Platform Operator: %w", err)
		}
		if rowEmail == email {
			if subject != "" && subject != identity.Subject {
				return "", false, ErrDenied
			}
			emailOperatorID = id
		}
		if subject == identity.Subject {
			operatorID = id
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, fmt.Errorf("iterate Platform Operators: %w", err)
	}
	rows.Close()
	if operatorID != "" {
		return operatorID, true, nil
	}
	if emailOperatorID == "" {
		return "", false, nil
	}
	if err := tx.QueryRow(ctx, `
		UPDATE access_platform_operators
		SET user_subject = $2
		WHERE id = $1 AND user_subject IS NULL
		RETURNING id::text
	`, emailOperatorID, identity.Subject).Scan(&operatorID); err != nil {
		return "", false, fmt.Errorf("bind Platform Operator: %w", err)
	}
	return operatorID, true, nil
}

func lockPlatformOperatorIdentity(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
) error {
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(1094927189, hashtext($1))
	`, identity.Subject); err != nil {
		return fmt.Errorf("lock Platform Operator subject: %w", err)
	}
	return lockPlatformOperatorEmail(ctx, tx, identity.Email)
}

func lockPlatformOperatorEmail(
	ctx context.Context,
	tx pgx.Tx,
	email string,
) error {
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(1094927188, hashtext($1))
	`, normalizeEmail(email)); err != nil {
		return fmt.Errorf("lock Platform Operator identity: %w", err)
	}
	return nil
}

func loadOperatorAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	practiceID string,
) (Authorization, error) {
	result := Authorization{
		Actor: Actor{
			Subject: identity.Subject,
			Email:   normalizeEmail(identity.Email),
			Type:    "PLATFORM_OPERATOR",
		},
		PlatformOperator: true,
	}
	if err := tx.QueryRow(ctx, `
		SELECT id::text, name, workspace_version
		FROM access_practices
		WHERE id = $1
	`, practiceID).Scan(
		&result.Practice.ID,
		&result.Practice.Name,
		&result.Practice.Version,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Authorization{}, ErrDenied
		}
		return Authorization{}, fmt.Errorf("resolve operator Practice: %w", err)
	}
	locations, err := loadLocations(ctx, tx, practiceID)
	if err != nil {
		return Authorization{}, err
	}
	result.Locations = locations
	return result, nil
}

func loadLocations(ctx context.Context, tx pgx.Tx, practiceID string) ([]Location, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, name
		FROM access_locations
		WHERE practice_id = $1
		ORDER BY name, id
	`, practiceID)
	if err != nil {
		return nil, fmt.Errorf("load Practice Locations: %w", err)
	}
	defer rows.Close()
	locations := []Location{}
	for rows.Next() {
		var location Location
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			return nil, fmt.Errorf("scan Practice Location: %w", err)
		}
		locations = append(locations, location)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Practice Locations: %w", err)
	}
	return locations, nil
}

// AuditOperatorMutation records a Platform Operator mutation in the caller's
// transaction. Practice User mutations do not need an operator audit row.
func (m *Module) AuditOperatorMutation(
	ctx context.Context,
	tx pgx.Tx,
	authorization Authorization,
	audit OperatorMutationAudit,
) error {
	if !authorization.PlatformOperator {
		return nil
	}
	if tx == nil ||
		authorization.Actor.Type != "PLATFORM_OPERATOR" ||
		strings.TrimSpace(authorization.Actor.Subject) == "" ||
		strings.TrimSpace(authorization.Practice.ID) == "" ||
		strings.TrimSpace(audit.Action) == "" ||
		strings.TrimSpace(audit.ResourceType) == "" ||
		strings.TrimSpace(audit.ResourceID) == "" ||
		audit.ResourceVersion <= 0 ||
		audit.OccurredAt.IsZero() {
		return ErrDenied
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_audit_events (
			actor_type,
			actor_subject,
			practice_id,
			action,
			details,
			created_at
		)
		VALUES (
			'PLATFORM_OPERATOR',
			$1,
			$2,
			$3,
			jsonb_build_object(
				'resourceType', $4::text,
				'resourceId', $5::text,
				'resourceVersion', $6::bigint
			),
			$7
		)
	`,
		authorization.Actor.Subject,
		authorization.Practice.ID,
		strings.TrimSpace(audit.Action),
		strings.TrimSpace(audit.ResourceType),
		strings.TrimSpace(audit.ResourceID),
		audit.ResourceVersion,
		audit.OccurredAt,
	); err != nil {
		return fmt.Errorf("audit operator %s mutation: %w", audit.ResourceType, err)
	}
	return nil
}

// RecordWorkspaceChange advances the authoritative Practice workspace version
// and publishes a disposable refetch hint in the caller's transaction.
func (m *Module) RecordWorkspaceChange(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
) (int64, error) {
	if tx == nil || strings.TrimSpace(practiceID) == "" {
		return 0, ErrDenied
	}
	var version int64
	if err := tx.QueryRow(ctx, `
		UPDATE access_practices
		SET workspace_version = workspace_version + 1
		WHERE id = $1
		RETURNING workspace_version
	`, practiceID).Scan(&version); err != nil {
		return 0, fmt.Errorf("increment workspace version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT pg_notify(
			'acuity_workspace_hints',
			json_build_object(
				'practiceId', id,
				'version', workspace_version
			)::text
		)
		FROM access_practices
		WHERE id = $1
	`, practiceID); err != nil {
		return 0, fmt.Errorf("publish workspace hint: %w", err)
	}
	return version, nil
}

func selectRequestedLocation(authorization *Authorization, locationID string) error {
	if strings.TrimSpace(locationID) == "" {
		return nil
	}
	for _, location := range authorization.Locations {
		if location.ID == locationID {
			selected := location
			authorization.ActiveLocation = &selected
			return nil
		}
	}
	return ErrDenied
}

func loadMembershipAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	practiceID string,
	lockMembership bool,
) (Authorization, error) {
	var result Authorization
	result.Actor = Actor{
		Subject: identity.Subject,
		Email:   normalizeEmail(identity.Email),
		Type:    "HUMAN",
	}
	membershipQuery := `
		SELECT
			m.id::text,
			m.role,
			m.location_scope,
			p.id::text,
			p.name,
			p.workspace_version
		FROM access_memberships m
		JOIN access_practices p ON p.id = m.practice_id
		WHERE
			m.user_subject = $1
			AND m.practice_id = $2
			AND m.revoked_at IS NULL
	`
	if lockMembership {
		membershipQuery += `FOR SHARE OF m`
	}
	if err := tx.QueryRow(ctx, membershipQuery, identity.Subject, practiceID).Scan(
		&result.Membership.ID,
		&result.Membership.Role,
		&result.Membership.LocationScope,
		&result.Practice.ID,
		&result.Practice.Name,
		&result.Practice.Version,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Authorization{}, ErrDenied
		}
		return Authorization{}, fmt.Errorf("resolve membership: %w", err)
	}

	locationQuery := `
		SELECT l.id::text, l.name
		FROM access_locations l
		WHERE l.practice_id = $1
		ORDER BY l.name, l.id
	`
	args := []any{practiceID}
	if result.Membership.LocationScope == LocationScopeSelected {
		locationQuery = `
			SELECT l.id::text, l.name
			FROM access_membership_locations ml
			JOIN access_locations l
				ON l.practice_id = ml.practice_id
				AND l.id = ml.location_id
			WHERE ml.membership_id = $1
			ORDER BY l.name, l.id
		`
		args = []any{result.Membership.ID}
	}
	rows, err := tx.Query(ctx, locationQuery, args...)
	if err != nil {
		return Authorization{}, fmt.Errorf("resolve membership locations: %w", err)
	}
	defer rows.Close()
	result.Locations = []Location{}
	for rows.Next() {
		var location Location
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			return Authorization{}, fmt.Errorf("scan membership location: %w", err)
		}
		result.Locations = append(result.Locations, location)
	}
	if err := rows.Err(); err != nil {
		return Authorization{}, fmt.Errorf("iterate membership locations: %w", err)
	}
	return result, nil
}

func validateProvisioning(input Provisioning) error {
	if strings.TrimSpace(input.Environment) == "" || strings.TrimSpace(input.RequestedBy) == "" {
		return fmt.Errorf("%w: environment and requestedBy are required", ErrInvalidInput)
	}
	if len(input.Practices) == 0 {
		return fmt.Errorf("%w: at least one practice is required", ErrInvalidInput)
	}
	operatorEmails := make(map[string]struct{}, len(input.PlatformOperators))
	for _, email := range input.PlatformOperators {
		operatorEmails[normalizeEmail(email)] = struct{}{}
	}
	for _, practice := range input.Practices {
		if strings.TrimSpace(practice.Key) == "" || strings.TrimSpace(practice.Name) == "" {
			return fmt.Errorf("%w: practice key and name are required", ErrInvalidInput)
		}
		officeKeys := make(map[string]struct{})
		for _, location := range practice.Locations {
			if strings.TrimSpace(location.Key) == "" || strings.TrimSpace(location.Name) == "" {
				return fmt.Errorf("%w: location key and name are required", ErrInvalidInput)
			}
			for _, officeKey := range location.AbitaOfficeKeys {
				if !abitaOfficeKey.MatchString(officeKey) {
					return fmt.Errorf("%w: invalid Abita office key", ErrInvalidInput)
				}
				if _, duplicate := officeKeys[officeKey]; duplicate {
					return fmt.Errorf("%w: duplicate Abita office key", ErrInvalidInput)
				}
				officeKeys[officeKey] = struct{}{}
			}
			if input.Environment != "test" && input.Environment != "development" &&
				strings.HasPrefix(strings.ToLower(location.Name), "fixture ") {
				return fmt.Errorf("%w: fixture locations are forbidden outside test/development", ErrInvalidInput)
			}
		}
		for _, grant := range practice.AccessGrants {
			if _, isOperator := operatorEmails[normalizeEmail(grant.Email)]; isOperator {
				return fmt.Errorf("%w: Platform Operators do not use Access Grants", ErrInvalidInput)
			}
			if grant.Role != RoleAdmin && grant.Role != RoleStaff {
				return fmt.Errorf("%w: unsupported Access Grant role", ErrInvalidInput)
			}
			if grant.Role == RoleAdmin && grant.LocationScope != LocationScopeAll {
				return fmt.Errorf("%w: Admin requires ALL location scope", ErrInvalidInput)
			}
			if grant.LocationScope != LocationScopeAll && grant.LocationScope != LocationScopeSelected {
				return fmt.Errorf("%w: unsupported Location scope", ErrInvalidInput)
			}
			if grant.LocationScope == LocationScopeSelected && len(grant.SelectedLocationKeys) == 0 {
				return fmt.Errorf("%w: SELECTED scope requires a Location", ErrInvalidInput)
			}
		}
	}
	return nil
}

func serviceHasCapability(
	identity ServiceIdentity,
	capability ServiceCapability,
) bool {
	for _, candidate := range identity.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
