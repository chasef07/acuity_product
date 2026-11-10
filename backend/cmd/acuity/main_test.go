package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/app"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestProductionProvisioningBuildsAbitaAndIsolatedDemoTopology(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	output := filepath.Join(t.TempDir(), "provisioning-output.json")
	input := filepath.Join("..", "..", "..", "config", "production-provisioning.json")

	if err := runMigrate(context.Background(), app.Config{
		ProvisioningInput:  input,
		ProvisioningOutput: output,
	}, pool); err != nil {
		t.Fatalf("run production provisioning: %v", err)
	}

	var ringGroups, nmbOnlyMember int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (
			WHERE location.provisioning_key = 'north-miami-beach-optical'
			AND ring_group.member_emails = ARRAY[access_grant.email]
		)
		FROM human_calling_location_ring_groups ring_group
		JOIN access_locations location ON location.id = ring_group.location_id
		JOIN access_grants access_grant ON access_grant.practice_id = ring_group.practice_id
			AND access_grant.provisioning_key = 'bright-vu-miami'
	`).Scan(&ringGroups, &nmbOnlyMember); err != nil || ringGroups != 1 || nmbOnlyMember != 1 {
		t.Fatalf("production ring groups=%d NMB single-member groups=%d err=%v", ringGroups, nmbOnlyMember, err)
	}

	operatorEmails := []string{}
	operatorRows, err := pool.Query(context.Background(), `
		SELECT email FROM access_platform_operators ORDER BY email
	`)
	if err != nil {
		t.Fatalf("read Platform Operators: %v", err)
	}
	for operatorRows.Next() {
		var email string
		if err := operatorRows.Scan(&email); err != nil {
			operatorRows.Close()
			t.Fatalf("scan Platform Operator: %v", err)
		}
		operatorEmails = append(operatorEmails, email)
	}
	if err := operatorRows.Err(); err != nil {
		operatorRows.Close()
		t.Fatalf("iterate Platform Operators: %v", err)
	}
	operatorRows.Close()
	wantOperatorEmails := []string{
		"chase@acuityhealth.io",
		"kyle@acuityhealth.io",
	}
	if !reflect.DeepEqual(operatorEmails, wantOperatorEmails) {
		t.Fatalf("Platform Operators = %#v, want %#v", operatorEmails, wantOperatorEmails)
	}

	type route struct {
		Practice string
		Office   string
		Location string
	}
	routes := []route{}
	rows, err := pool.Query(context.Background(), `
		SELECT practice.provisioning_key, route.office_key, location.provisioning_key
		FROM access_abita_office_locations route
		JOIN access_practices practice ON practice.id = route.practice_id
		JOIN access_locations location
			ON location.practice_id = route.practice_id
			AND location.id = route.location_id
		ORDER BY route.office_key
	`)
	if err != nil {
		t.Fatalf("read office routes: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var candidate route
		if err := rows.Scan(&candidate.Practice, &candidate.Office, &candidate.Location); err != nil {
			t.Fatalf("scan office route: %v", err)
		}
		routes = append(routes, candidate)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate office routes: %v", err)
	}
	wantRoutes := []route{
		{Practice: "abita-eye-group", Office: "crystal-river", Location: "crystal-river"},
		{Practice: "acuity-demo", Office: "dev", Location: "demo-484"},
		{Practice: "abita-eye-group", Office: "hollywood", Location: "hollywood"},
		{Practice: "acuity-demo", Office: "mental-health-demo", Location: "new-tampa-demo"},
		{Practice: "acuity-demo", Office: "new-tampa-demo", Location: "new-tampa-demo"},
		{Practice: "abita-eye-group", Office: "north-miami-beach-optical", Location: "north-miami-beach-optical"},
		{Practice: "acuity-demo", Office: "ophthalmology-demo", Location: "ophthalmology-demo"},
		{Practice: "acuity-demo", Office: "rheumatology-demo", Location: "demo-484"},
		{Practice: "abita-eye-group", Office: "spring-hill", Location: "spring-hill"},
		{Practice: "abita-eye-group", Office: "sweetwater", Location: "sweetwater"},
		{Practice: "abita-eye-group", Office: "sweetwater-optical", Location: "sweetwater-optical"},
	}
	if !reflect.DeepEqual(routes, wantRoutes) {
		t.Fatalf("office routes = %#v, want %#v", routes, wantRoutes)
	}

	type location struct {
		Practice string
		Key      string
		Name     string
	}
	locations := []location{}
	rows, err = pool.Query(context.Background(), `
		SELECT practice.provisioning_key, location.provisioning_key, location.name
		FROM access_locations location
		JOIN access_practices practice ON practice.id = location.practice_id
		ORDER BY practice.provisioning_key, location.provisioning_key
	`)
	if err != nil {
		t.Fatalf("read provisioned Locations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var candidate location
		if err := rows.Scan(&candidate.Practice, &candidate.Key, &candidate.Name); err != nil {
			t.Fatalf("scan provisioned Location: %v", err)
		}
		locations = append(locations, candidate)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate provisioned Locations: %v", err)
	}
	wantLocations := []location{
		{Practice: "abita-eye-group", Key: "crystal-river", Name: "Crystal River"},
		{Practice: "abita-eye-group", Key: "hollywood", Name: "Hollywood"},
		{Practice: "abita-eye-group", Key: "north-miami-beach-optical", Name: "North Miami Beach Optical"},
		{Practice: "abita-eye-group", Key: "spring-hill", Name: "Spring Hill"},
		{Practice: "abita-eye-group", Key: "sweetwater", Name: "Sweetwater"},
		{Practice: "abita-eye-group", Key: "sweetwater-optical", Name: "Sweetwater Optical"},
		{Practice: "acuity-demo", Key: "demo-484", Name: "Rheumatology"},
		{Practice: "acuity-demo", Key: "new-tampa-demo", Name: "New Tampa Eye Institute"},
		{Practice: "acuity-demo", Key: "ophthalmology-demo", Name: "Ophthalmology"},
	}
	if !reflect.DeepEqual(locations, wantLocations) {
		t.Fatalf("Locations = %#v, want %#v", locations, wantLocations)
	}

	discovery, err := access.New(db.As(testdb.Portal), nil).DiscoverActor(
		context.Background(),
		access.Identity{
			Subject:       "production-topology-test",
			Email:         "chase@acuityhealth.io",
			EmailVerified: true,
		},
	)
	if err != nil {
		t.Fatalf("discover production selector topology: %v", err)
	}
	var demoLocationNames []string
	for _, practice := range discovery.Practices {
		if practice.Name != "Acuity Demo" {
			continue
		}
		for _, location := range practice.Locations {
			demoLocationNames = append(demoLocationNames, location.Name)
		}
	}
	wantDemoLocationNames := []string{"New Tampa Eye Institute", "Ophthalmology", "Rheumatology"}
	if !reflect.DeepEqual(demoLocationNames, wantDemoLocationNames) {
		t.Fatalf(
			"Acuity Demo selector Locations = %#v, want %#v",
			demoLocationNames,
			wantDemoLocationNames,
		)
	}

	type number struct {
		Practice string
		Location string
		Phone    string
	}
	loadNumbers := func(query string) []number {
		t.Helper()
		configured := []number{}
		rows, err := pool.Query(context.Background(), query)
		if err != nil {
			t.Fatalf("read configured numbers: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var candidate number
			if err := rows.Scan(&candidate.Practice, &candidate.Location, &candidate.Phone); err != nil {
				t.Fatalf("scan configured number: %v", err)
			}
			configured = append(configured, candidate)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate configured numbers: %v", err)
		}
		return configured
	}
	voiceNumbers := loadNumbers(`
		SELECT practice.provisioning_key, location.provisioning_key, voice.phone
		FROM human_calling_location_voice_numbers voice
		JOIN access_practices practice ON practice.id = voice.practice_id
		JOIN access_locations location
			ON location.practice_id = voice.practice_id
			AND location.id = voice.location_id
		WHERE voice.enabled
		ORDER BY practice.provisioning_key, location.provisioning_key
	`)
	wantVoiceNumbers := []number{
		{Practice: "abita-eye-group", Location: "crystal-river", Phone: "+13523202007"},
		{Practice: "abita-eye-group", Location: "hollywood", Phone: "+19542872010"},
		{Practice: "abita-eye-group", Location: "north-miami-beach-optical", Phone: "+13055095333"},
		{Practice: "abita-eye-group", Location: "spring-hill", Phone: "+17275919997"},
		{Practice: "abita-eye-group", Location: "sweetwater", Phone: "+17864654836"},
		{Practice: "acuity-demo", Location: "demo-484", Phone: "+14843989071"},
		{Practice: "acuity-demo", Location: "new-tampa-demo", Phone: "+13207388132"},
		{Practice: "acuity-demo", Location: "ophthalmology-demo", Phone: "+18027878312"},
	}
	if !reflect.DeepEqual(voiceNumbers, wantVoiceNumbers) {
		t.Fatalf("voice numbers = %#v, want %#v", voiceNumbers, wantVoiceNumbers)
	}

	var fallbackPractice, fallbackLocation string
	if err := pool.QueryRow(context.Background(), `
		SELECT practice.provisioning_key, location.provisioning_key
		FROM human_calling_outbound_voice_fallbacks fallback
		JOIN access_practices practice ON practice.id = fallback.practice_id
		JOIN access_locations location
			ON location.practice_id = fallback.practice_id
			AND location.id = fallback.location_id
	`).Scan(&fallbackPractice, &fallbackLocation); err != nil {
		t.Fatalf("read outbound voice fallback: %v", err)
	}
	if fallbackPractice != "abita-eye-group" || fallbackLocation != "sweetwater" {
		t.Fatalf(
			"outbound voice fallback = %q/%q, want abita-eye-group/sweetwater",
			fallbackPractice,
			fallbackLocation,
		)
	}

	type messagingConfiguration struct {
		Practice string
		Location string
		Sender   string
		Profile  string
		Active   bool
	}
	messagingConfigurations := []messagingConfiguration{}
	rows, err = pool.Query(context.Background(), `
		SELECT practice.provisioning_key, location.provisioning_key,
			messaging.sender, messaging.messaging_profile_id, messaging.active
		FROM messaging_location_configurations messaging
		JOIN access_practices practice ON practice.id = messaging.practice_id
		JOIN access_locations location
			ON location.practice_id = messaging.practice_id
			AND location.id = messaging.location_id
		ORDER BY practice.provisioning_key, location.provisioning_key
	`)
	if err != nil {
		t.Fatalf("read Messaging configurations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var configured messagingConfiguration
		if err := rows.Scan(
			&configured.Practice,
			&configured.Location,
			&configured.Sender,
			&configured.Profile,
			&configured.Active,
		); err != nil {
			t.Fatalf("scan Messaging configuration: %v", err)
		}
		messagingConfigurations = append(messagingConfigurations, configured)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate Messaging configurations: %v", err)
	}
	wantMessagingConfigurations := []messagingConfiguration{
		{
			Practice: "abita-eye-group",
			Location: "hollywood",
			Sender:   "+19542872010",
			Profile:  "40019fbc-d47c-4e6c-86ee-87ab00795371",
			Active:   true,
		},
		{
			Practice: "abita-eye-group",
			Location: "north-miami-beach-optical",
			Sender:   "+13055095333",
			Profile:  "40019fbc-d47c-4e6c-86ee-87ab00795371",
			Active:   true,
		},
		{
			Practice: "abita-eye-group",
			Location: "spring-hill",
			Sender:   "+17275919997",
			Profile:  "40019fbc-d47c-4e6c-86ee-87ab00795371",
			Active:   true,
		},
		{
			Practice: "abita-eye-group",
			Location: "sweetwater",
			Sender:   "+17864654836",
			Profile:  "40019fbc-d47c-4e6c-86ee-87ab00795371",
			Active:   true,
		},
		{
			Practice: "acuity-demo",
			Location: "demo-484",
			Sender:   "+14843989071",
			Profile:  "40019fbc-d47c-4e6c-86ee-87ab00795371",
			Active:   true,
		},
	}
	if !reflect.DeepEqual(messagingConfigurations, wantMessagingConfigurations) {
		t.Fatalf(
			"Messaging configurations = %#v, want %#v",
			messagingConfigurations,
			wantMessagingConfigurations,
		)
	}

	const sharedGreeting = "Thank you for calling Abeeta Eye Group. You have reached us after hours, or we are with a patient, and we are not able to answer your call. Please leave your information after the tone and we will return the call promptly. If this is an emergency after hours, please call our on-call physician at 727-379-4923."
	const southFloridaGreeting = "Thank you for calling Abeeta Eye Group. You have reached us after hours, or we are with a patient, and we are not able to answer your call. Please leave your information after the tone and we will return the call promptly."
	const southFloridaAfterHoursGreeting = southFloridaGreeting + " If this is an emergency, please call 305-741-0830."
	const demoGreeting = "Thank you for calling Acuity Demo. Please leave a message after the tone."
	type greetingConfiguration struct {
		Practice   string
		Location   string
		Greeting   string
		AfterHours string
	}
	greetings := []greetingConfiguration{}
	rows, err = pool.Query(context.Background(), `
		SELECT practice.provisioning_key, location.provisioning_key,
			voice.voicemail_greeting, COALESCE(voice.after_hours_voicemail_greeting, '')
		FROM human_calling_location_voice_numbers voice
		JOIN access_practices practice ON practice.id = voice.practice_id
		JOIN access_locations location
			ON location.practice_id = voice.practice_id
			AND location.id = voice.location_id
		WHERE voice.enabled
		ORDER BY practice.provisioning_key, location.provisioning_key
	`)
	if err != nil {
		t.Fatalf("read voicemail greetings: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var configured greetingConfiguration
		if err := rows.Scan(
			&configured.Practice,
			&configured.Location,
			&configured.Greeting,
			&configured.AfterHours,
		); err != nil {
			t.Fatalf("scan voicemail greeting: %v", err)
		}
		greetings = append(greetings, configured)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate voicemail greetings: %v", err)
	}
	wantGreetings := []greetingConfiguration{
		{Practice: "abita-eye-group", Location: "crystal-river", Greeting: sharedGreeting},
		{Practice: "abita-eye-group", Location: "hollywood", Greeting: southFloridaGreeting, AfterHours: southFloridaAfterHoursGreeting},
		{Practice: "abita-eye-group", Location: "north-miami-beach-optical", Greeting: southFloridaGreeting},
		{Practice: "abita-eye-group", Location: "spring-hill", Greeting: sharedGreeting},
		{Practice: "abita-eye-group", Location: "sweetwater", Greeting: southFloridaGreeting, AfterHours: southFloridaAfterHoursGreeting},
		{Practice: "acuity-demo", Location: "demo-484", Greeting: demoGreeting},
		{Practice: "acuity-demo", Location: "new-tampa-demo", Greeting: "Please leave a message after the beep."},
		{Practice: "acuity-demo", Location: "ophthalmology-demo", Greeting: "Please leave a message after the beep."},
	}
	if !reflect.DeepEqual(greetings, wantGreetings) {
		t.Fatalf("voicemail greetings = %#v, want %#v", greetings, wantGreetings)
	}

	var grantCount, abitaGrantCount, demoGrantCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*),
			count(*) FILTER (WHERE practice.provisioning_key = 'abita-eye-group'),
			count(*) FILTER (
				WHERE practice.provisioning_key = 'acuity-demo'
					AND access_grant.email IN (
						'chase@acuityhealth.io',
						'kyle@acuityhealth.io'
					)
					AND access_grant.role = 'STAFF'
			)
		FROM access_grants access_grant
		JOIN access_practices practice ON practice.id = access_grant.practice_id
	`).Scan(&grantCount, &abitaGrantCount, &demoGrantCount); err != nil {
		t.Fatalf("count provisioned Access Grants: %v", err)
	}
	if grantCount != 33 || abitaGrantCount != 33 || demoGrantCount != 0 {
		t.Fatalf("provisioned Access Grants = total:%d Abita:%d demo:%d, want 33, 33, 0",
			grantCount, abitaGrantCount, demoGrantCount)
	}
	var brightVuEmail, brightVuRole, brightVuScope, brightVuLocation string
	if err := pool.QueryRow(context.Background(), `
		SELECT access_grant.email, access_grant.role, access_grant.location_scope,
			location.provisioning_key
		FROM access_grants access_grant
		JOIN access_grant_locations allowed ON allowed.access_grant_id = access_grant.id
		JOIN access_locations location ON location.id = allowed.location_id
		WHERE access_grant.provisioning_key = 'bright-vu-miami'
	`).Scan(&brightVuEmail, &brightVuRole, &brightVuScope, &brightVuLocation); err != nil {
		t.Fatalf("read Bright Vu Miami Access Grant: %v", err)
	}
	if brightVuEmail != "brightvumiami@gmail.com" || brightVuRole != "STAFF" ||
		brightVuScope != "SELECTED" || brightVuLocation != "north-miami-beach-optical" {
		t.Fatalf(
			"Bright Vu Miami Access Grant = email:%q role:%q scope:%q location:%q",
			brightVuEmail, brightVuRole, brightVuScope, brightVuLocation,
		)
	}
	type grant struct {
		Email string
		Role  string
		Scope string
	}
	grants := []grant{}
	rows, err = pool.Query(context.Background(), `
		SELECT email, role::text, location_scope::text
		FROM access_grants
		WHERE email IN ('aileen@abitaeye.com', 'telemed@abitaeye.com')
		ORDER BY email
	`)
	if err != nil {
		t.Fatalf("read representative Access Grants: %v", err)
	}
	for rows.Next() {
		var candidate grant
		if err := rows.Scan(&candidate.Email, &candidate.Role, &candidate.Scope); err != nil {
			rows.Close()
			t.Fatalf("scan representative Access Grant: %v", err)
		}
		grants = append(grants, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate representative Access Grants: %v", err)
	}
	rows.Close()
	wantGrants := []grant{
		{Email: "aileen@abitaeye.com", Role: "STAFF", Scope: "SELECTED"},
		{Email: "telemed@abitaeye.com", Role: "ADMIN", Scope: "ALL"},
	}
	if !reflect.DeepEqual(grants, wantGrants) {
		t.Fatalf("representative Access Grants = %#v, want %#v", grants, wantGrants)
	}
	var aileenLocationCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM access_grant_locations grant_location
		JOIN access_grants access_grant ON access_grant.id = grant_location.access_grant_id
		WHERE access_grant.email = 'aileen@abitaeye.com'
	`).Scan(&aileenLocationCount); err != nil {
		t.Fatalf("count Aileen Locations: %v", err)
	}
	if aileenLocationCount != 4 {
		t.Fatalf("Aileen Locations = %d, want 4", aileenLocationCount)
	}
	var hollywoodGrantCount, sweetwaterOpticalGrantCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE location.provisioning_key = 'hollywood'),
			count(*) FILTER (WHERE location.provisioning_key = 'sweetwater-optical')
		FROM access_grants access_grant
		JOIN access_grant_locations allowed ON allowed.access_grant_id = access_grant.id
		JOIN access_locations location ON location.id = allowed.location_id
		WHERE access_grant.provisioning_key IN (
			'ari-nussbaum',
			'denise-rivera',
			'katie-einsohn',
			'sasha-ojinaga'
		)
	`).Scan(&hollywoodGrantCount, &sweetwaterOpticalGrantCount); err != nil {
		t.Fatalf("count expanded Hollywood Access Grants: %v", err)
	}
	if hollywoodGrantCount != 4 || sweetwaterOpticalGrantCount != 4 {
		t.Fatalf(
			"expanded Access Grant Locations = Hollywood:%d Sweetwater Optical:%d",
			hollywoodGrantCount, sweetwaterOpticalGrantCount,
		)
	}
	var provisioned access.Provisioned
	outputFile, err := os.Open(output)
	if err != nil {
		t.Fatalf("open provisioning output: %v", err)
	}
	defer outputFile.Close()
	if err := json.NewDecoder(outputFile).Decode(&provisioned); err != nil {
		t.Fatalf("decode provisioning output: %v", err)
	}
	if provisioned.AccessGrantCount != 33 {
		t.Fatalf("provisioning output Access Grant count = %d, want 33", provisioned.AccessGrantCount)
	}
}

func TestEveryReleaseReappliesProductionProvisioning(t *testing.T) {
	pool := testdb.Open(t).Owner
	directory := t.TempDir()
	input := filepath.Join("..", "..", "..", "config", "production-provisioning.json")
	release := func(output string) access.Provisioned {
		t.Helper()
		path := filepath.Join(directory, output)
		if err := runMigrate(context.Background(), app.Config{
			ProvisioningInput:  input,
			ProvisioningOutput: path,
		}, pool); err != nil {
			t.Fatalf("release %s: %v", output, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", output, err)
		}
		var provisioned access.Provisioned
		if err := json.Unmarshal(raw, &provisioned); err != nil {
			t.Fatalf("decode %s: %v", output, err)
		}
		return provisioned
	}
	release("first.json")
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_location_voice_numbers voice
		SET voicemail_greeting = 'Stale greeting.', after_hours_voicemail_greeting = NULL
		FROM access_locations location
		WHERE location.id = voice.location_id AND location.provisioning_key = 'hollywood'
	`); err != nil {
		t.Fatalf("stale Hollywood greeting: %v", err)
	}

	if created := release("second.json").AccessGrantCount; created != 0 {
		t.Fatalf("second release created %d Access Grants, want 0", created)
	}
	var greeting, afterHours string
	if err := pool.QueryRow(context.Background(), `
		SELECT voice.voicemail_greeting, voice.after_hours_voicemail_greeting
		FROM human_calling_location_voice_numbers voice
		JOIN access_locations location ON location.id = voice.location_id
		WHERE location.provisioning_key = 'hollywood'
	`).Scan(&greeting, &afterHours); err != nil {
		t.Fatalf("read Hollywood greetings: %v", err)
	}
	if !strings.HasSuffix(greeting, "we will return the call promptly.") ||
		!strings.HasSuffix(afterHours, "please call 305-741-0830.") {
		t.Fatalf("Hollywood greetings = %q / %q", greeting, afterHours)
	}
}

func TestEveryReleaseLoadsTheTaskResponsibilityRoster(t *testing.T) {
	pool := testdb.Open(t).Owner
	directory := t.TempDir()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	release := func(output string) (work.ResponsibilityReport, string) {
		t.Helper()
		path := filepath.Join(directory, output)
		if err := runMigrate(context.Background(), app.Config{
			ProvisioningInput:     filepath.Join("..", "..", "..", "config", "production-provisioning.json"),
			ProvisioningOutput:    path,
			ResponsibilitiesInput: filepath.Join("..", "..", "..", "config", "task-responsibilities.json"),
		}, pool); err != nil {
			t.Fatalf("release %s: %v", output, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", output, err)
		}
		var written struct {
			AccessGrantCount *int                       `json:"accessGrantCount"`
			Responsibilities *work.ResponsibilityReport `json:"responsibilities"`
		}
		if err := json.Unmarshal(raw, &written); err != nil {
			t.Fatalf("decode %s: %v", output, err)
		}
		if written.AccessGrantCount == nil || written.Responsibilities == nil {
			t.Fatalf("%s lost the grant count or the roster report: %s", output, raw)
		}
		var rows string
		if err := pool.QueryRow(context.Background(), `
			SELECT string_agg(concat_ws(':', location.provisioning_key, roster.account_email, roster.category, roster.role), ','
				ORDER BY location.provisioning_key, roster.account_email, roster.category, roster.role)
			FROM work_responsibilities roster
			JOIN access_locations location ON location.id = roster.location_id
		`).Scan(&rows); err != nil {
			t.Fatalf("read roster rows: %v", err)
		}
		return *written.Responsibilities, rows
	}

	first, firstRows := release("first.json")
	t.Logf("production roster report: applied=%d unmatched=%v coverageGaps=%v", first.Applied, first.Unmatched, first.CoverageGaps)
	if first.Applied != 98 || len(first.Unmatched) != 8 || len(first.CoverageGaps) != 13 {
		t.Fatalf("roster report = applied %d, unmatched %d, coverage gaps %d; want 98, 8, 13",
			first.Applied, len(first.Unmatched), len(first.CoverageGaps))
	}
	if loaded := strings.Count(firstRows, ",") + 1; loaded != first.Applied {
		t.Fatalf("roster rows = %d, report applied %d", loaded, first.Applied)
	}
	var configuredLocations int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM work_responsibility_locations`).Scan(&configuredLocations); err != nil || configuredLocations != 4 {
		t.Fatalf("configured roster Locations = %d, %v; want 4", configuredLocations, err)
	}
	if !strings.Contains(logs.String(), `"msg":"migrations_applied","provisioning":true,"access_grant_count":33,"responsibilities_applied":98,"responsibilities_unmatched":8,"responsibilities_coverage_gaps":13`) {
		t.Fatalf("release log lost the roster counts: %s", logs.String())
	}

	second, secondRows := release("second.json")
	if !reflect.DeepEqual(second, first) || secondRows != firstRows {
		t.Fatalf("second release changed the roster: report %#v -> %#v", first, second)
	}
}

func TestMigrateRollsBackProvisioningWhenTheRosterNamesAnUnknownLocation(t *testing.T) {
	pool := testdb.Open(t).Owner
	directory := t.TempDir()
	roster := filepath.Join(directory, "roster.json")
	if err := os.WriteFile(roster, []byte(`{"PracticeKey":"abita-eye-group","Locations":[{"LocationKey":"closed-office","Members":[]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "provisioning-output.json")
	err := runMigrate(context.Background(), app.Config{
		ProvisioningInput:     filepath.Join("..", "..", "..", "config", "production-provisioning.json"),
		ProvisioningOutput:    output,
		ResponsibilitiesInput: roster,
	}, pool)
	if err == nil || !strings.Contains(err.Error(), "closed-office") {
		t.Fatalf("release error = %v, want the unknown roster Location", err)
	}
	var grants int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM access_grants`).Scan(&grants); err != nil || grants != 0 {
		t.Fatalf("Access Grants committed beside a failed roster: %d, %v", grants, err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed release kept its output: %v", err)
	}
}

func TestMigrateRollsBackProvisioningWhenVoiceConfigurationFails(t *testing.T) {
	pool := testdb.Open(t).Owner
	directory := t.TempDir()
	input := filepath.Join(directory, "invalid-provisioning.json")
	file, err := os.Create(input)
	if err != nil {
		t.Fatalf("create invalid provisioning input: %v", err)
	}
	if err := json.NewEncoder(file).Encode(access.Provisioning{
		Environment:             "production",
		RequestedBy:             "atomic-provisioning-test",
		RequireEmptyAccessState: true,
		Practices: []access.PracticeProvision{{
			Key:  "atomic-test",
			Name: "Atomic Test",
			Locations: []access.LocationProvision{{
				Key:         "invalid-voice",
				Name:        "Invalid Voice",
				VoiceNumber: "+1",
			}},
		}},
	}); err != nil {
		_ = file.Close()
		t.Fatalf("write invalid provisioning input: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close invalid provisioning input: %v", err)
	}
	output := filepath.Join(directory, "provisioning-output.json")

	err = runMigrate(context.Background(), app.Config{
		ProvisioningInput:  input,
		ProvisioningOutput: output,
	}, pool)
	if err == nil {
		t.Fatal("migrate accepted invalid voice configuration")
	}
	var practiceCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM access_practices
	`).Scan(&practiceCount); err != nil {
		t.Fatalf("count Practices after failed migrate: %v", err)
	}
	if practiceCount != 0 {
		t.Fatalf("Practices after failed migrate = %d, want 0", practiceCount)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed migrate retained provisioning output: %v", err)
	}
}
