package humancalling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

type mediaTokenProvider struct {
	*recordingProvider
	now      time.Time
	tokenErr error
}

func (provider *mediaTokenProvider) Execute(ctx context.Context, command humancalling.ProviderCommand) (humancalling.ProviderResult, error) {
	if command.Action != humancalling.CommandCreateJWT {
		return provider.recordingProvider.Execute(ctx, command)
	}
	if provider.tokenErr != nil {
		return humancalling.ProviderResult{}, provider.tokenErr
	}
	return humancalling.ProviderResult{JWT: "media-jwt", JWTExpiresAt: provider.now.Add(time.Hour)}, nil
}

func TestMediaTokenIssueLeavesNoProviderCommandBehind(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	provider := &mediaTokenProvider{recordingProvider: &recordingProvider{}, now: now}
	fixture := newOutboundEndFixture(t, "media-token", now, provider)
	commandRows := func() int {
		t.Helper()
		var rows int
		if err := fixture.pool.QueryRow(ctx, `
			SELECT count(*) FROM human_calling_provider_commands WHERE action = 'CREATE_JWT'
		`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}

	provider.tokenErr = errors.New("synthetic provider outage")
	if _, err := fixture.calling.IssueMediaJWT(ctx, fixture.identity, fixture.sessionID); !errors.Is(err, provider.tokenErr) {
		t.Fatalf("transient provider failure = %v, want the provider error", err)
	}
	if rows := commandRows(); rows != 0 {
		t.Fatalf("failed issue left %d CREATE_JWT rows", rows)
	}

	provider.tokenErr = nil
	token, err := fixture.calling.IssueMediaJWT(ctx, fixture.identity, fixture.sessionID)
	if err != nil || token.Token != "media-jwt" || !token.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("issue media token = %#v, %v", token, err)
	}
	if rows := commandRows(); rows != 0 {
		t.Fatalf("successful issue left %d CREATE_JWT rows", rows)
	}
}
