package main

import (
	"context"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/app"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

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
