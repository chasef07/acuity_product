package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/app"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProductionProvisioningBuildsAbitaAndIsolatedDemoTopology(t *testing.T) {
	pool := testdb.Open(t)
	ensureRuntimeRoles(t, pool)
	output := filepath.Join(t.TempDir(), "provisioning-output.json")
	input := filepath.Join("..", "..", "..", "config", "production-provisioning.json")

	if err := runMigrate(context.Background(), app.Config{
		ProvisioningInput:  input,
		ProvisioningOutput: output,
	}, pool); err != nil {
		t.Fatalf("run production provisioning: %v", err)
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
		{Practice: "abita-eye-group", Office: "north-miami-beach-optical", Location: "north-miami-beach-optical"},
		{Practice: "abita-eye-group", Office: "spring-hill", Location: "spring-hill"},
		{Practice: "abita-eye-group", Office: "sweetwater", Location: "sweetwater"},
		{Practice: "abita-eye-group", Office: "sweetwater-optical", Location: "sweetwater-optical"},
	}
	if !reflect.DeepEqual(routes, wantRoutes) {
		t.Fatalf("office routes = %#v, want %#v", routes, wantRoutes)
	}

	locations := []string{}
	rows, err = pool.Query(context.Background(), `
		SELECT practice.provisioning_key || '/' || location.provisioning_key
		FROM access_locations location
		JOIN access_practices practice ON practice.id = location.practice_id
		ORDER BY practice.provisioning_key, location.provisioning_key
	`)
	if err != nil {
		t.Fatalf("read provisioned Locations: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var location string
		if err := rows.Scan(&location); err != nil {
			t.Fatalf("scan provisioned Location: %v", err)
		}
		locations = append(locations, location)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate provisioned Locations: %v", err)
	}
	wantLocations := []string{
		"abita-eye-group/crystal-river",
		"abita-eye-group/hollywood",
		"abita-eye-group/north-miami-beach-optical",
		"abita-eye-group/spring-hill",
		"abita-eye-group/sweetwater",
		"abita-eye-group/sweetwater-optical",
		"acuity-demo/demo-484",
	}
	if !reflect.DeepEqual(locations, wantLocations) {
		t.Fatalf("Locations = %#v, want %#v", locations, wantLocations)
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
		{Practice: "abita-eye-group", Location: "north-miami-beach-optical", Phone: "+13055095333"},
		{Practice: "abita-eye-group", Location: "spring-hill", Phone: "+17275919997"},
		{Practice: "abita-eye-group", Location: "sweetwater", Phone: "+17864654836"},
		{Practice: "acuity-demo", Location: "demo-484", Phone: "+14843989071"},
	}
	if !reflect.DeepEqual(voiceNumbers, wantVoiceNumbers) {
		t.Fatalf("voice numbers = %#v, want %#v", voiceNumbers, wantVoiceNumbers)
	}

	type messagingConfiguration struct {
		Practice string
		Location string
		Sender   string
		Profile  string
	}
	messagingConfigurations := []messagingConfiguration{}
	rows, err = pool.Query(context.Background(), `
		SELECT practice.provisioning_key, location.provisioning_key,
			messaging.sender, messaging.messaging_profile_id
		FROM messaging_location_configurations messaging
		JOIN access_practices practice ON practice.id = messaging.practice_id
		JOIN access_locations location
			ON location.practice_id = messaging.practice_id
			AND location.id = messaging.location_id
		WHERE messaging.active
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
			Location: "spring-hill",
			Sender:   "+17275919997",
			Profile:  "40019e5f-3772-4c25-a9df-b0990499d947",
		},
		{
			Practice: "abita-eye-group",
			Location: "sweetwater",
			Sender:   "+17864654836",
			Profile:  "40019e5f-3772-4c25-a9df-b0990499d947",
		},
		{
			Practice: "acuity-demo",
			Location: "demo-484",
			Sender:   "+14843989071",
			Profile:  "40019fbc-d47c-4e6c-86ee-87ab00795371",
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
	const demoGreeting = "Thank you for calling Acuity Demo. Please leave a message after the tone."
	type greetingConfiguration struct {
		Practice string
		Location string
		Greeting string
	}
	greetings := []greetingConfiguration{}
	rows, err = pool.Query(context.Background(), `
		SELECT practice.provisioning_key, location.provisioning_key,
			voice.voicemail_greeting
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
		{Practice: "abita-eye-group", Location: "north-miami-beach-optical", Greeting: sharedGreeting},
		{Practice: "abita-eye-group", Location: "spring-hill", Greeting: sharedGreeting},
		{Practice: "abita-eye-group", Location: "sweetwater", Greeting: sharedGreeting},
		{Practice: "acuity-demo", Location: "demo-484", Greeting: demoGreeting},
	}
	if !reflect.DeepEqual(greetings, wantGreetings) {
		t.Fatalf("voicemail greetings = %#v, want %#v", greetings, wantGreetings)
	}

	var invitationCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM access_invitations
	`).Scan(&invitationCount); err != nil {
		t.Fatalf("count provisioned invitations: %v", err)
	}
	if invitationCount != 0 {
		t.Fatalf("provisioned invitations = %d, want 0", invitationCount)
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
	if len(provisioned.Invitations) != 0 {
		t.Fatalf("provisioning output invitations = %d, want 0", len(provisioned.Invitations))
	}
}

func ensureRuntimeRoles(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		DO $roles$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'acuity_auth') THEN
				CREATE ROLE acuity_auth NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'acuity_portal') THEN
				CREATE ROLE acuity_portal NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'acuity_provider') THEN
				CREATE ROLE acuity_provider NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'acuity_realtime') THEN
				CREATE ROLE acuity_realtime NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'acuity_worker') THEN
				CREATE ROLE acuity_worker NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'acuity_migrate') THEN
				CREATE ROLE acuity_migrate NOLOGIN;
			END IF;
		END
		$roles$;
	`); err != nil {
		t.Fatalf("ensure runtime roles: %v", err)
	}
}

func TestProductionProvisioningRejectsLegacyTestConfiguration(t *testing.T) {
	pool := testdb.Open(t)
	ensureRuntimeRoles(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('abita-eye-group', 'Abita Eye Group')
	`); err != nil {
		t.Fatalf("seed legacy Practice: %v", err)
	}

	err := runMigrate(context.Background(), app.Config{
		ProvisioningInput: filepath.Join(
			"..", "..", "..", "config", "production-provisioning.json",
		),
		ProvisioningOutput: filepath.Join(t.TempDir(), "provisioning-output.json"),
	}, pool)
	if err == nil || !strings.Contains(err.Error(), "requires empty Access state") {
		t.Fatalf("legacy production provisioning error = %v", err)
	}

	var practiceCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM access_practices
	`).Scan(&practiceCount); err != nil {
		t.Fatalf("count Practices after rejected provisioning: %v", err)
	}
	if practiceCount != 1 {
		t.Fatalf("Practices after rejected provisioning = %d, want 1", practiceCount)
	}
}

func TestMigrateRollsBackProvisioningWhenVoiceConfigurationFails(t *testing.T) {
	pool := testdb.Open(t)
	ensureRuntimeRoles(t, pool)
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

func TestProvisionConfiguredLocationVoiceReconcilesCallerID(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('abita-eye-group', 'Abita Eye Group')
		RETURNING id::text
	`).Scan(&practiceID); err != nil {
		t.Fatalf("seed live demo Practice: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		VALUES ($1, 'demo-484', 'Demo — 484')
		RETURNING id::text
	`, practiceID).Scan(&locationID); err != nil {
		t.Fatalf("seed live demo Location: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_location_voice_numbers (
			practice_id,
			location_id,
			phone,
			enabled
		)
		VALUES ($1, $2, '+14845550100', true)
	`, practiceID, locationID); err != nil {
		t.Fatalf("seed prior live demo voice route: %v", err)
	}

	if err := provisionConfiguredLocationVoice(ctx, app.Config{
		LocationVoiceProvision: app.LocationVoiceProvisionConfig{
			PracticeKey: "abita-eye-group",
			LocationKey: "demo-484",
			Number:      "+14843989071",
		},
	}, pool); err != nil {
		t.Fatalf("provision configured Location voice: %v", err)
	}
	var enabledPhone string
	if err := pool.QueryRow(ctx, `
		SELECT phone
		FROM human_calling_location_voice_numbers
		WHERE practice_id = $1
			AND location_id = $2
			AND enabled
	`, practiceID, locationID).Scan(&enabledPhone); err != nil {
		t.Fatalf("read live demo caller ID: %v", err)
	}
	if enabledPhone != "+14843989071" {
		t.Fatalf("live demo caller ID = %q", enabledPhone)
	}
}

func TestProvisionConfiguredLocationVoiceRejectsMissingLocation(t *testing.T) {
	pool := testdb.Open(t)
	err := provisionConfiguredLocationVoice(context.Background(), app.Config{
		LocationVoiceProvision: app.LocationVoiceProvisionConfig{
			PracticeKey: "abita-eye-group",
			LocationKey: "demo-484",
			Number:      "+14843989071",
		},
	}, pool)
	if err == nil {
		t.Fatal("provisioning accepted a missing configured Location")
	}
	if !strings.Contains(err.Error(), `provision Location voice "abita-eye-group"/"demo-484"`) {
		t.Fatalf("missing configured Location error = %q", err)
	}
}
