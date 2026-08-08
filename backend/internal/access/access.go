package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	ErrDenied                  = errors.New("access denied")
	ErrEmailNotVerified        = errors.New("verified email required")
	ErrInvitationExpired       = errors.New("invitation expired")
	ErrInvitationRevoked       = errors.New("invitation revoked")
	ErrInvitationUsed          = errors.New("invitation already accepted")
	ErrInvalidInput            = errors.New("invalid access input")
	ErrSupportRequired         = errors.New("active Support Mode required")
	ErrSupportExpired          = errors.New("Support Mode expired")
	ErrSupportRevoked          = errors.New("Support Mode revoked")
	ErrSupportPracticeMismatch = errors.New("Support Mode belongs to another Practice")
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
	Key         string
	Name        string
	Locations   []LocationProvision
	Invitations []InvitationProvision
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

type InvitationProvision struct {
	Key                  string
	Email                string
	Role                 Role
	LocationScope        LocationScope
	SelectedLocationKeys []string
	ExpiresAt            time.Time
}

type Provisioned struct {
	Invitations []InvitationCredential `json:"invitations"`
}

type InvitationCredential struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Token string `json:"token"`
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
	Actor            Actor        `json:"actor"`
	Practice         Practice     `json:"practice"`
	Membership       Membership   `json:"membership"`
	Locations        []Location   `json:"locations"`
	ActiveLocation   *Location    `json:"activeLocation,omitempty"`
	PlatformOperator bool         `json:"platformOperator"`
	SupportMode      *SupportMode `json:"supportMode,omitempty"`
}

type PracticeAccess struct {
	Practice
	Membership *Membership `json:"membership,omitempty"`
	Locations  []Location  `json:"locations"`
}

type Discovery struct {
	Actor            Actor            `json:"actor"`
	PlatformOperator bool             `json:"platformOperator"`
	Practices        []PracticeAccess `json:"practices"`
}

type SupportMode struct {
	ID         string    `json:"id"`
	PracticeID string    `json:"practiceId"`
	Reason     string    `json:"reason"`
	StartsAt   time.Time `json:"startsAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type EnterSupportModeCommand struct {
	Identity   Identity
	PracticeID string
	Reason     string
	Duration   time.Duration
}

type AddLocationCommand struct {
	Identity         Identity
	PracticeID       string
	SupportSessionID string
	Key              string
	Name             string
}

type RevokeInvitationCommand struct {
	Identity         Identity
	PracticeID       string
	SupportSessionID string
	InvitationID     string
}

type RevokeMembershipCommand struct {
	Identity         Identity
	PracticeID       string
	SupportSessionID string
	MembershipID     string
}

type AuditEvent struct {
	ID               string    `json:"id"`
	ActorSubject     string    `json:"actorSubject"`
	PracticeID       string    `json:"practiceId"`
	SupportSessionID string    `json:"supportSessionId,omitempty"`
	Action           string    `json:"action"`
	Reason           string    `json:"reason,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

type SupportedMutationAudit struct {
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

type InvitationKind string

const (
	InvitationKindPractice         InvitationKind = "PRACTICE_INVITATION"
	InvitationKindPlatformOperator InvitationKind = "PLATFORM_OPERATOR"
)

type InvitationInspection struct {
	Token string
	Email string
}

type InvitationPreview struct {
	Kind          InvitationKind `json:"kind"`
	Email         string         `json:"email"`
	PracticeID    string         `json:"practiceId,omitempty"`
	PracticeName  string         `json:"practiceName,omitempty"`
	Role          Role           `json:"role,omitempty"`
	LocationScope LocationScope  `json:"locationScope,omitempty"`
	Locations     []Location     `json:"locations"`
	ExpiresAt     time.Time      `json:"expiresAt,omitempty"`
}

// Module is the Access implementation. Its public methods are the product
// interface; focused PostgreSQL behavior remains local to this package.
type Module struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func New(pool *pgxpool.Pool, now func() time.Time) *Module {
	if now == nil {
		now = time.Now
	}
	return &Module{pool: pool, now: now}
}

func (m *Module) Provision(ctx context.Context, input Provisioning) (Provisioned, error) {
	if m.pool == nil {
		return Provisioned{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
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
	if err := validateProvisioning(input, m.now()); err != nil {
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

	result := Provisioned{Invitations: []InvitationCredential{}}
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
		var practiceID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO access_practices (provisioning_key, name)
			VALUES ($1, $2)
			ON CONFLICT (provisioning_key) DO UPDATE SET name = EXCLUDED.name
			RETURNING id::text
		`, practiceInput.Key, practiceInput.Name).Scan(&practiceID); err != nil {
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

		for _, invitationInput := range practiceInput.Invitations {
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM access_invitations
					WHERE practice_id = $1 AND provisioning_key = $2
				)
			`, practiceID, invitationInput.Key).Scan(&exists); err != nil {
				return Provisioned{}, fmt.Errorf("check invitation %q: %w", invitationInput.Key, err)
			}
			if exists {
				continue
			}

			token, tokenHash, err := newInvitationToken()
			if err != nil {
				return Provisioned{}, err
			}
			email := normalizeEmail(invitationInput.Email)
			var invitationID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO access_invitations (
					provisioning_key, practice_id, token_hash, email, role,
					location_scope, expires_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				RETURNING id::text
			`,
				invitationInput.Key,
				practiceID,
				tokenHash[:],
				email,
				invitationInput.Role,
				invitationInput.LocationScope,
				invitationInput.ExpiresAt,
			).Scan(&invitationID); err != nil {
				return Provisioned{}, fmt.Errorf("create invitation %q: %w", invitationInput.Key, err)
			}
			for _, locationKey := range invitationInput.SelectedLocationKeys {
				locationID, ok := locationIDs[locationKey]
				if !ok {
					return Provisioned{}, fmt.Errorf("%w: invitation %q references location %q", ErrInvalidInput, invitationInput.Key, locationKey)
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO access_invitation_locations (
						invitation_id, location_id, practice_id
					)
					VALUES ($1, $2, $3)
				`, invitationID, locationID, practiceID); err != nil {
					return Provisioned{}, fmt.Errorf("grant invitation location %q: %w", locationKey, err)
				}
			}
			result.Invitations = append(result.Invitations, InvitationCredential{
				ID:    invitationID,
				Email: email,
				Token: token,
			})
		}

		details, _ := json.Marshal(map[string]any{
			"practiceKey": practiceInput.Key,
			"locations":   len(practiceInput.Locations),
			"invitations": len(practiceInput.Invitations),
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

func (m *Module) AcceptInvitation(ctx context.Context, identity Identity, token string) (Authorization, error) {
	if !identity.EmailVerified {
		return Authorization{}, ErrEmailNotVerified
	}
	if strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(token) == "" {
		return Authorization{}, ErrDenied
	}

	tokenHash := sha256.Sum256([]byte(token))
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Authorization{}, fmt.Errorf("begin invitation acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var invitationID, practiceID, invitationEmail, acceptedSubject string
	var role Role
	var scope LocationScope
	var expiresAt time.Time
	var revokedAt, acceptedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
			id::text,
			practice_id::text,
			email,
			role,
			location_scope,
			expires_at,
			revoked_at,
			accepted_at,
			COALESCE(accepted_by_subject, '')
		FROM access_invitations
		WHERE token_hash = $1
		FOR UPDATE
	`, tokenHash[:]).Scan(
		&invitationID,
		&practiceID,
		&invitationEmail,
		&role,
		&scope,
		&expiresAt,
		&revokedAt,
		&acceptedAt,
		&acceptedSubject,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Authorization{}, ErrDenied
		}
		return Authorization{}, fmt.Errorf("load invitation: %w", err)
	}

	if revokedAt != nil {
		return Authorization{}, ErrInvitationRevoked
	}
	if !m.now().Before(expiresAt) {
		return Authorization{}, ErrInvitationExpired
	}
	if normalizeEmail(identity.Email) != invitationEmail {
		return Authorization{}, ErrDenied
	}
	if acceptedAt != nil {
		if acceptedSubject != identity.Subject {
			return Authorization{}, ErrInvitationUsed
		}
		authorized, err := loadMembershipAuthorization(ctx, tx, identity, practiceID)
		if err != nil {
			return Authorization{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Authorization{}, fmt.Errorf("commit invitation replay: %w", err)
		}
		return authorized, nil
	}

	var membershipID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO access_memberships (
			user_subject, email, practice_id, role, location_scope, invitation_id
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text
	`, identity.Subject, invitationEmail, practiceID, role, scope, invitationID).Scan(&membershipID); err != nil {
		return Authorization{}, fmt.Errorf("create membership: %w", err)
	}
	if scope == LocationScopeSelected {
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_membership_locations (membership_id, location_id, practice_id)
			SELECT $1, location_id, practice_id
			FROM access_invitation_locations
			WHERE invitation_id = $2
		`, membershipID, invitationID); err != nil {
			return Authorization{}, fmt.Errorf("copy selected location grants: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE access_invitations
		SET accepted_at = $2, accepted_by_subject = $3
		WHERE id = $1
	`, invitationID, m.now(), identity.Subject); err != nil {
		return Authorization{}, fmt.Errorf("mark invitation accepted: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_audit_events (
			actor_type, actor_subject, practice_id, action, details
		)
		VALUES (
			'HUMAN', $1, $2, 'invitation.accepted',
			jsonb_build_object(
				'invitationId', $3::text,
				'membershipId', $4::text
			)
		)
	`, identity.Subject, practiceID, invitationID, membershipID); err != nil {
		return Authorization{}, fmt.Errorf("audit invitation acceptance: %w", err)
	}
	if _, err := m.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return Authorization{}, err
	}

	authorized, err := loadMembershipAuthorization(ctx, tx, identity, practiceID)
	if err != nil {
		return Authorization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Authorization{}, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	return authorized, nil
}

// InspectInvitation is used by the invite-only authentication hook and invite
// screen. It returns only scope attached to the presented credential.
func (m *Module) InspectInvitation(
	ctx context.Context,
	inspection InvitationInspection,
) (InvitationPreview, error) {
	email := normalizeEmail(inspection.Email)
	if strings.TrimSpace(inspection.Token) == "" {
		if email == "" {
			return InvitationPreview{}, ErrDenied
		}
		var exists bool
		if err := m.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM access_platform_operators
				WHERE email = $1
			)
		`, email).Scan(&exists); err != nil {
			return InvitationPreview{}, fmt.Errorf("inspect Platform Operator eligibility: %w", err)
		}
		if !exists {
			return InvitationPreview{}, ErrDenied
		}
		return InvitationPreview{
			Kind:      InvitationKindPlatformOperator,
			Email:     email,
			Locations: []Location{},
		}, nil
	}

	tokenHash := sha256.Sum256([]byte(inspection.Token))
	var preview InvitationPreview
	var revokedAt, acceptedAt *time.Time
	if err := m.pool.QueryRow(ctx, `
		SELECT
			i.email,
			i.practice_id::text,
			p.name,
			i.role,
			i.location_scope,
			i.expires_at,
			i.revoked_at,
			i.accepted_at
		FROM access_invitations i
		JOIN access_practices p ON p.id = i.practice_id
		WHERE i.token_hash = $1
	`, tokenHash[:]).Scan(
		&preview.Email,
		&preview.PracticeID,
		&preview.PracticeName,
		&preview.Role,
		&preview.LocationScope,
		&preview.ExpiresAt,
		&revokedAt,
		&acceptedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return InvitationPreview{}, ErrDenied
		}
		return InvitationPreview{}, fmt.Errorf("inspect invitation: %w", err)
	}
	if revokedAt != nil {
		return InvitationPreview{}, ErrInvitationRevoked
	}
	if acceptedAt != nil {
		return InvitationPreview{}, ErrInvitationUsed
	}
	if !m.now().Before(preview.ExpiresAt) {
		return InvitationPreview{}, ErrInvitationExpired
	}
	if email != "" && email != preview.Email {
		return InvitationPreview{}, ErrDenied
	}
	preview.Kind = InvitationKindPractice
	preview.Locations = []Location{}

	locationQuery := `
		SELECT id::text, name
		FROM access_locations
		WHERE practice_id = $1
		ORDER BY name, id
	`
	args := []any{preview.PracticeID}
	if preview.LocationScope == LocationScopeSelected {
		locationQuery = `
			SELECT l.id::text, l.name
			FROM access_invitation_locations il
			JOIN access_locations l
				ON l.practice_id = il.practice_id
				AND l.id = il.location_id
			JOIN access_invitations i ON i.id = il.invitation_id
			WHERE i.token_hash = $1
			ORDER BY l.name, l.id
		`
		args = []any{tokenHash[:]}
	}
	rows, err := m.pool.Query(ctx, locationQuery, args...)
	if err != nil {
		return InvitationPreview{}, fmt.Errorf("inspect invitation Locations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var location Location
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			return InvitationPreview{}, fmt.Errorf("scan invitation Location: %w", err)
		}
		preview.Locations = append(preview.Locations, location)
	}
	if err := rows.Err(); err != nil {
		return InvitationPreview{}, fmt.Errorf("iterate invitation Locations: %w", err)
	}
	return preview, nil
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
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Authorization{}, fmt.Errorf("begin actor resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	operatorID, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return Authorization{}, err
	}
	if isOperator {
		authorized, err := loadOperatorAuthorization(ctx, tx, identity, operatorID, practiceID, m.now())
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

	authorized, err := loadMembershipAuthorization(ctx, tx, identity, practiceID)
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
	if tx == nil ||
		!identity.EmailVerified ||
		strings.TrimSpace(identity.Subject) == "" ||
		strings.TrimSpace(practiceID) == "" ||
		strings.TrimSpace(locationID) == "" {
		return Authorization{}, ErrDenied
	}
	if _, isOperator, err := bindPlatformOperator(ctx, tx, identity); err != nil {
		return Authorization{}, err
	} else if isOperator {
		return Authorization{}, ErrDenied
	}
	var membershipID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM access_memberships
		WHERE user_subject = $1
			AND practice_id = $2
			AND revoked_at IS NULL
		FOR SHARE
	`, identity.Subject, practiceID).Scan(&membershipID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Authorization{}, ErrDenied
		}
		return Authorization{}, fmt.Errorf("lock Membership authorization: %w", err)
	}
	authorization, err := loadMembershipAuthorization(ctx, tx, identity, practiceID)
	if err != nil {
		return Authorization{}, err
	}
	if authorization.Membership.ID != membershipID {
		return Authorization{}, ErrDenied
	}
	if err := selectRequestedLocation(&authorization, locationID); err != nil {
		return Authorization{}, err
	}
	return authorization, nil
}

// LockMutationAuthorization resolves current customer-data mutation authority
// inside a caller-owned transaction. Practice Users use their current
// Membership; Platform Operators additionally require active Support Mode.
func (m *Module) LockMutationAuthorization(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	practiceID string,
	locationID string,
	supportSessionID string,
) (Authorization, error) {
	if tx == nil ||
		!identity.EmailVerified ||
		strings.TrimSpace(identity.Subject) == "" ||
		strings.TrimSpace(practiceID) == "" ||
		strings.TrimSpace(locationID) == "" {
		return Authorization{}, ErrDenied
	}
	operatorID, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return Authorization{}, err
	}
	if !isOperator {
		return m.LockMembershipAuthorization(
			ctx,
			tx,
			identity,
			practiceID,
			locationID,
		)
	}
	if supportSessionID == "" {
		return Authorization{}, ErrSupportRequired
	}
	support, err := m.authorizeSupportMutation(
		ctx,
		tx,
		identity,
		practiceID,
		supportSessionID,
	)
	if err != nil {
		return Authorization{}, err
	}
	authorization, err := loadOperatorAuthorization(
		ctx,
		tx,
		identity,
		operatorID,
		practiceID,
		m.now(),
	)
	if err != nil {
		return Authorization{}, err
	}
	if err := selectRequestedLocation(&authorization, locationID); err != nil {
		return Authorization{}, err
	}
	authorization.SupportMode = &support
	return authorization, nil
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
	if tx == nil ||
		!identity.EmailVerified ||
		strings.TrimSpace(identity.Subject) == "" ||
		strings.TrimSpace(practiceID) == "" {
		return Authorization{}, ErrDenied
	}
	operatorID, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return Authorization{}, err
	}
	if isOperator {
		authorization, err := loadOperatorAuthorization(
			ctx,
			tx,
			identity,
			operatorID,
			practiceID,
			m.now(),
		)
		if err != nil {
			return Authorization{}, err
		}
		if err := selectRequestedLocation(&authorization, locationID); err != nil {
			return Authorization{}, err
		}
		return authorization, nil
	}

	var membershipID string
	if err := tx.QueryRow(ctx, `
		SELECT id::text
		FROM access_memberships
		WHERE user_subject = $1
			AND practice_id = $2
			AND revoked_at IS NULL
		FOR SHARE
	`, identity.Subject, practiceID).Scan(&membershipID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Authorization{}, ErrDenied
		}
		return Authorization{}, fmt.Errorf("lock Membership read authorization: %w", err)
	}
	authorization, err := loadMembershipAuthorization(ctx, tx, identity, practiceID)
	if err != nil {
		return Authorization{}, err
	}
	if authorization.Membership.ID != membershipID {
		return Authorization{}, ErrDenied
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
		FOR SHARE OF voice, location
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

// LockOperationalActor holds the actor's current Memberships against revocation,
// returns their Practice IDs, and applies Platform Operator precedence.
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
	if _, isOperator, err := bindPlatformOperator(ctx, tx, identity); err != nil {
		return nil, err
	} else if isOperator {
		return nil, ErrDenied
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

// DiscoverActor returns every currently authorized Practice and Location. A
// Platform Operator's global visibility does not imply mutation authority.
func (m *Module) DiscoverActor(ctx context.Context, identity Identity) (Discovery, error) {
	if !identity.EmailVerified || strings.TrimSpace(identity.Subject) == "" {
		return Discovery{}, ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Discovery{}, fmt.Errorf("begin actor discovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	operatorID, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return Discovery{}, err
	}
	if !isOperator {
		rows, err := tx.Query(ctx, `
			SELECT practice_id::text
			FROM access_memberships
			WHERE user_subject = $1 AND revoked_at IS NULL
			ORDER BY practice_id
		`, identity.Subject)
		if err != nil {
			return Discovery{}, fmt.Errorf("discover Memberships: %w", err)
		}
		practiceIDs := []string{}
		for rows.Next() {
			var practiceID string
			if err := rows.Scan(&practiceID); err != nil {
				rows.Close()
				return Discovery{}, fmt.Errorf("scan discovered Membership: %w", err)
			}
			practiceIDs = append(practiceIDs, practiceID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return Discovery{}, fmt.Errorf("iterate discovered Memberships: %w", err)
		}
		rows.Close()
		if len(practiceIDs) == 0 {
			return Discovery{}, ErrDenied
		}
		result := Discovery{
			Actor: Actor{
				Subject: identity.Subject,
				Email:   normalizeEmail(identity.Email),
				Type:    "HUMAN",
			},
			Practices: []PracticeAccess{},
		}
		for _, practiceID := range practiceIDs {
			authorization, err := loadMembershipAuthorization(ctx, tx, identity, practiceID)
			if err != nil {
				return Discovery{}, err
			}
			membership := authorization.Membership
			result.Practices = append(result.Practices, PracticeAccess{
				Practice:   authorization.Practice,
				Membership: &membership,
				Locations:  authorization.Locations,
			})
		}
		sort.Slice(result.Practices, func(i, j int) bool {
			if result.Practices[i].Name == result.Practices[j].Name {
				return result.Practices[i].ID < result.Practices[j].ID
			}
			return result.Practices[i].Name < result.Practices[j].Name
		})
		if err := tx.Commit(ctx); err != nil {
			return Discovery{}, fmt.Errorf("commit member discovery: %w", err)
		}
		return result, nil
	}
	_ = operatorID

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
		result.Practices = append(result.Practices, practice)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Discovery{}, fmt.Errorf("iterate discovered Practices: %w", err)
	}
	rows.Close()
	for index := range result.Practices {
		result.Practices[index].Locations, err = loadLocations(ctx, tx, result.Practices[index].ID)
		if err != nil {
			return Discovery{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Discovery{}, fmt.Errorf("commit actor discovery: %w", err)
	}
	return result, nil
}

func (m *Module) EnterSupportMode(
	ctx context.Context,
	command EnterSupportModeCommand,
) (SupportMode, error) {
	reason := strings.TrimSpace(command.Reason)
	if reason == "" || command.Duration <= 0 || command.Duration > 4*time.Hour {
		return SupportMode{}, fmt.Errorf("%w: Support Mode needs a reason and duration no longer than four hours", ErrInvalidInput)
	}
	if !command.Identity.EmailVerified || command.Identity.Subject == "" || command.PracticeID == "" {
		return SupportMode{}, ErrDenied
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SupportMode{}, fmt.Errorf("begin Support Mode: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operatorID, isOperator, err := bindPlatformOperator(ctx, tx, command.Identity)
	if err != nil {
		return SupportMode{}, err
	}
	if !isOperator {
		return SupportMode{}, ErrDenied
	}
	var practiceExists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM access_practices WHERE id = $1)`,
		command.PracticeID,
	).Scan(&practiceExists); err != nil {
		return SupportMode{}, fmt.Errorf("check Support Mode Practice: %w", err)
	}
	if !practiceExists {
		return SupportMode{}, ErrDenied
	}

	startsAt := m.now()
	result := SupportMode{
		PracticeID: command.PracticeID,
		Reason:     reason,
		StartsAt:   startsAt,
		ExpiresAt:  startsAt.Add(command.Duration),
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO access_support_sessions (
			platform_operator_id, practice_id, reason, starts_at, expires_at
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text
	`, operatorID, command.PracticeID, reason, result.StartsAt, result.ExpiresAt).Scan(&result.ID); err != nil {
		return SupportMode{}, fmt.Errorf("create Support Mode: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_audit_events (
			actor_type, actor_subject, practice_id, support_session_id,
			action, reason
		)
		VALUES ('PLATFORM_OPERATOR', $1, $2, $3, 'support.entered', $4)
	`, command.Identity.Subject, command.PracticeID, result.ID, reason); err != nil {
		return SupportMode{}, fmt.Errorf("audit Support Mode: %w", err)
	}
	if _, err := m.RecordWorkspaceChange(ctx, tx, command.PracticeID); err != nil {
		return SupportMode{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SupportMode{}, fmt.Errorf("commit Support Mode: %w", err)
	}
	return result, nil
}

func (m *Module) RevokeSupportMode(
	ctx context.Context,
	identity Identity,
	supportSessionID string,
) error {
	if !identity.EmailVerified || identity.Subject == "" || supportSessionID == "" {
		return ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Support Mode revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	operatorID, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return err
	}
	if !isOperator {
		return ErrDenied
	}

	var sessionOperatorID, practiceID, reason string
	var revokedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
			platform_operator_id::text,
			practice_id::text,
			reason,
			revoked_at
		FROM access_support_sessions
		WHERE id = $1
		FOR UPDATE
	`, supportSessionID).Scan(
		&sessionOperatorID,
		&practiceID,
		&reason,
		&revokedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDenied
		}
		return fmt.Errorf("load Support Mode for revocation: %w", err)
	}
	if sessionOperatorID != operatorID {
		return ErrDenied
	}
	if revokedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit repeated Support Mode revocation: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE access_support_sessions
		SET revoked_at = $2
		WHERE id = $1
	`, supportSessionID, m.now()); err != nil {
		return fmt.Errorf("revoke Support Mode: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_audit_events (
			actor_type, actor_subject, practice_id, support_session_id,
			action, reason
		)
		VALUES ('PLATFORM_OPERATOR', $1, $2, $3, 'support.revoked', $4)
	`, identity.Subject, practiceID, supportSessionID, reason); err != nil {
		return fmt.Errorf("audit Support Mode revocation: %w", err)
	}
	if _, err := m.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Support Mode revocation: %w", err)
	}
	return nil
}

func (m *Module) RevokeInvitation(
	ctx context.Context,
	command RevokeInvitationCommand,
) error {
	if command.InvitationID == "" {
		return ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin invitation revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	support, err := m.authorizeSupportMutation(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		command.SupportSessionID,
	)
	if err != nil {
		return err
	}

	var acceptedAt, revokedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT accepted_at, revoked_at
		FROM access_invitations
		WHERE id = $1 AND practice_id = $2
		FOR UPDATE
	`, command.InvitationID, command.PracticeID).Scan(&acceptedAt, &revokedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDenied
		}
		return fmt.Errorf("load invitation for revocation: %w", err)
	}
	if acceptedAt != nil {
		return ErrInvitationUsed
	}
	if revokedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit repeated invitation revocation: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE access_invitations
		SET revoked_at = $2
		WHERE id = $1
	`, command.InvitationID, m.now()); err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	if err := auditRevocation(
		ctx,
		tx,
		command.Identity.Subject,
		command.PracticeID,
		command.SupportSessionID,
		"invitation.revoked",
		support.Reason,
		"invitationId",
		command.InvitationID,
	); err != nil {
		return err
	}
	if _, err := m.RecordWorkspaceChange(ctx, tx, command.PracticeID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit invitation revocation: %w", err)
	}
	return nil
}

func (m *Module) RevokeMembership(
	ctx context.Context,
	command RevokeMembershipCommand,
) error {
	if command.MembershipID == "" {
		return ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Membership revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	support, err := m.authorizeSupportMutation(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		command.SupportSessionID,
	)
	if err != nil {
		return err
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
		command.SupportSessionID,
		"membership.revoked",
		support.Reason,
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
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
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
			COALESCE(support_session_id::text, ''),
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
			&event.SupportSessionID,
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
	if command.SupportSessionID == "" {
		return LocationMutation{}, ErrSupportRequired
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return LocationMutation{}, fmt.Errorf("begin Location mutation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	support, err := m.authorizeSupportMutation(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		command.SupportSessionID,
	)
	if err != nil {
		return LocationMutation{}, err
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
		ActorSubject:     command.Identity.Subject,
		PracticeID:       command.PracticeID,
		SupportSessionID: command.SupportSessionID,
		Action:           "location.added",
		Reason:           support.Reason,
		CreatedAt:        m.now(),
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO access_audit_events (
			actor_type, actor_subject, practice_id, support_session_id,
			action, reason, details, created_at
		)
		VALUES (
			'PLATFORM_OPERATOR', $1, $2, $3, $4, $5,
			jsonb_build_object('locationId', $6::text), $7
		)
		RETURNING id::text
	`,
		result.Audit.ActorSubject,
		result.Audit.PracticeID,
		result.Audit.SupportSessionID,
		result.Audit.Action,
		result.Audit.Reason,
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

func (m *Module) authorizeSupportMutation(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
	practiceID string,
	supportSessionID string,
) (SupportMode, error) {
	if !identity.EmailVerified || identity.Subject == "" || practiceID == "" {
		return SupportMode{}, ErrDenied
	}
	if supportSessionID == "" {
		return SupportMode{}, ErrSupportRequired
	}
	operatorID, isOperator, err := bindPlatformOperator(ctx, tx, identity)
	if err != nil {
		return SupportMode{}, err
	}
	if !isOperator {
		return SupportMode{}, ErrDenied
	}
	support := SupportMode{ID: supportSessionID}
	var supportOperatorID string
	var revokedAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT
			platform_operator_id::text,
			practice_id::text,
			reason,
			starts_at,
			expires_at,
			revoked_at
		FROM access_support_sessions
		WHERE id = $1
		FOR UPDATE
	`, supportSessionID).Scan(
		&supportOperatorID,
		&support.PracticeID,
		&support.Reason,
		&support.StartsAt,
		&support.ExpiresAt,
		&revokedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SupportMode{}, ErrSupportRequired
		}
		return SupportMode{}, fmt.Errorf("load Support Mode: %w", err)
	}
	if supportOperatorID != operatorID {
		return SupportMode{}, ErrDenied
	}
	if support.PracticeID != practiceID {
		return SupportMode{}, ErrSupportPracticeMismatch
	}
	if revokedAt != nil {
		return SupportMode{}, ErrSupportRevoked
	}
	if !m.now().Before(support.ExpiresAt) {
		return SupportMode{}, ErrSupportExpired
	}
	return support, nil
}

func auditRevocation(
	ctx context.Context,
	tx pgx.Tx,
	actorSubject string,
	practiceID string,
	supportSessionID string,
	action string,
	reason string,
	targetKey string,
	targetID string,
) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO access_audit_events (
			actor_type, actor_subject, practice_id, support_session_id,
			action, reason, details
		)
		VALUES (
			'PLATFORM_OPERATOR', $1, $2, $3, $4, $5,
			jsonb_build_object($6::text, $7::text)
		)
	`, actorSubject, practiceID, supportSessionID, action, reason, targetKey, targetID); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

func bindPlatformOperator(
	ctx context.Context,
	tx pgx.Tx,
	identity Identity,
) (string, bool, error) {
	if err := lockPlatformOperatorIdentity(ctx, tx, identity); err != nil {
		return "", false, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, email, COALESCE(user_subject, '')
		FROM access_platform_operators
		WHERE user_subject = $1 OR email = $2
		ORDER BY id
		FOR UPDATE
	`, identity.Subject, normalizeEmail(identity.Email))
	if err != nil {
		return "", false, fmt.Errorf("resolve Platform Operator: %w", err)
	}
	defer rows.Close()
	var operatorID, emailOperatorID string
	for rows.Next() {
		var id, email, subject string
		if err := rows.Scan(&id, &email, &subject); err != nil {
			return "", false, fmt.Errorf("scan Platform Operator: %w", err)
		}
		if email == normalizeEmail(identity.Email) {
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
	operatorID string,
	practiceID string,
	now time.Time,
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

	var support SupportMode
	err = tx.QueryRow(ctx, `
		SELECT id::text, practice_id::text, reason, starts_at, expires_at
		FROM access_support_sessions
		WHERE
			platform_operator_id = $1
			AND practice_id = $2
			AND revoked_at IS NULL
			AND expires_at > $3
		ORDER BY expires_at DESC
		LIMIT 1
	`, operatorID, practiceID, now).Scan(
		&support.ID,
		&support.PracticeID,
		&support.Reason,
		&support.StartsAt,
		&support.ExpiresAt,
	)
	if err == nil {
		result.SupportMode = &support
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Authorization{}, fmt.Errorf("resolve active Support Mode: %w", err)
	}
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

// AuditSupportedMutation records the Support Mode authorization used for a
// customer-data mutation in the caller's transaction. Practice User
// mutations do not need a Support Mode audit row.
func (m *Module) AuditSupportedMutation(
	ctx context.Context,
	tx pgx.Tx,
	authorization Authorization,
	audit SupportedMutationAudit,
) error {
	if !authorization.PlatformOperator {
		return nil
	}
	support := authorization.SupportMode
	if tx == nil ||
		support == nil ||
		authorization.Actor.Type != "PLATFORM_OPERATOR" ||
		strings.TrimSpace(authorization.Actor.Subject) == "" ||
		strings.TrimSpace(authorization.Practice.ID) == "" ||
		support.PracticeID != authorization.Practice.ID ||
		strings.TrimSpace(support.ID) == "" ||
		strings.TrimSpace(support.Reason) == "" ||
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
			support_session_id,
			action,
			reason,
			details,
			created_at
		)
		VALUES (
			'PLATFORM_OPERATOR',
			$1,
			$2,
			$3,
			$4,
			$5,
			jsonb_build_object(
				'resourceType', $6::text,
				'resourceId', $7::text,
				'resourceVersion', $8::bigint
			),
			$9
		)
	`,
		authorization.Actor.Subject,
		authorization.Practice.ID,
		support.ID,
		strings.TrimSpace(audit.Action),
		support.Reason,
		strings.TrimSpace(audit.ResourceType),
		strings.TrimSpace(audit.ResourceID),
		audit.ResourceVersion,
		audit.OccurredAt,
	); err != nil {
		return fmt.Errorf("audit supported %s mutation: %w", audit.ResourceType, err)
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
) (Authorization, error) {
	var result Authorization
	result.Actor = Actor{
		Subject: identity.Subject,
		Email:   normalizeEmail(identity.Email),
		Type:    "HUMAN",
	}
	if err := tx.QueryRow(ctx, `
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
	`, identity.Subject, practiceID).Scan(
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

func validateProvisioning(input Provisioning, now time.Time) error {
	if strings.TrimSpace(input.Environment) == "" || strings.TrimSpace(input.RequestedBy) == "" {
		return fmt.Errorf("%w: environment and requestedBy are required", ErrInvalidInput)
	}
	if len(input.Practices) == 0 {
		return fmt.Errorf("%w: at least one practice is required", ErrInvalidInput)
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
		for _, invitation := range practice.Invitations {
			if invitation.Role != RoleAdmin && invitation.Role != RoleStaff {
				return fmt.Errorf("%w: unsupported invitation role", ErrInvalidInput)
			}
			if invitation.Role == RoleAdmin && invitation.LocationScope != LocationScopeAll {
				return fmt.Errorf("%w: Admin requires ALL location scope", ErrInvalidInput)
			}
			if invitation.LocationScope != LocationScopeAll && invitation.LocationScope != LocationScopeSelected {
				return fmt.Errorf("%w: unsupported location scope", ErrInvalidInput)
			}
			if invitation.LocationScope == LocationScopeSelected && len(invitation.SelectedLocationKeys) == 0 {
				return fmt.Errorf("%w: SELECTED scope requires a location", ErrInvalidInput)
			}
			if !invitation.ExpiresAt.After(now) {
				return fmt.Errorf("%w: invitation expiration must be in the future", ErrInvalidInput)
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

func newInvitationToken() (string, [32]byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", [32]byte{}, fmt.Errorf("generate invitation token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, sha256.Sum256([]byte(token)), nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
