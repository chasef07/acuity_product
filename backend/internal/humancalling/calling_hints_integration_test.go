package humancalling_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCallingChangesPublishPracticeScopedCallingHints(t *testing.T) {
	pool, calling, _, staff := prepareInboundFanout(
		t, time.Now(), "calling-hints", &recordingProvider{}, 2,
	)
	listener := listenForHints(t, pool)

	processAllCommands(t, calling)
	state, err := calling.ReadCallingState(context.Background(), staff[0])
	if err != nil {
		t.Fatalf("read ringing Calling state: %v", err)
	}
	if len(state.Ringing) == 0 {
		t.Fatalf("ringing offers = %#v, want a visible offer", state.Ringing)
	}
	practiceID := state.Ringing[0].PracticeID
	if len(state.PracticeIDs) != 1 || state.PracticeIDs[0] != practiceID {
		t.Fatalf("Calling state Practices = %#v, want only %s", state.PracticeIDs, practiceID)
	}
	offered := drainHints(t, listener)
	offeredCalling := callingHintsFor(t, offered, practiceID)
	if offeredCalling == 0 || offeredCalling <= workspaceHints(offered) {
		t.Fatalf(
			"ring offer hints = %#v, want Calling hints beyond workspace version bumps",
			offered,
		)
	}

	if _, err := calling.AcquireSoftphone(
		context.Background(), staff[0], "calling-hints-takeover", true,
	); err != nil {
		t.Fatalf("take over softphone: %v", err)
	}
	takeover := drainHints(t, listener)
	if callingHintsFor(t, takeover, practiceID) == 0 || workspaceHints(takeover) == 0 {
		t.Fatalf("softphone takeover hints = %#v, want Calling and workspace hints", takeover)
	}
}

func listenForHints(t *testing.T, pool *pgxpool.Pool) *pgx.Conn {
	t.Helper()
	connection, err := pgx.ConnectConfig(context.Background(), pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatalf("connect hint listener: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close(context.Background()) })
	for _, channel := range []string{"acuity_workspace_hints", "acuity_calling_hints"} {
		if _, err := connection.Exec(context.Background(), "LISTEN "+channel); err != nil {
			t.Fatalf("listen for %s: %v", channel, err)
		}
	}
	return connection
}

func drainHints(t *testing.T, connection *pgx.Conn) []*pgconn.Notification {
	t.Helper()
	var notifications []*pgconn.Notification
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		notification, err := connection.WaitForNotification(ctx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) || (err != nil && ctx.Err() != nil) {
			return notifications
		}
		if err != nil {
			t.Fatalf("wait for hint: %v", err)
		}
		notifications = append(notifications, notification)
	}
}

func callingHintsFor(
	t *testing.T,
	notifications []*pgconn.Notification,
	practiceID string,
) int {
	t.Helper()
	count := 0
	for _, notification := range notifications {
		if notification.Channel != "acuity_calling_hints" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(notification.Payload), &payload); err != nil {
			t.Fatalf("decode Calling hint %q: %v", notification.Payload, err)
		}
		if len(payload) != 1 || payload["practiceId"] != practiceID {
			t.Fatalf("Calling hint payload = %q, want only practice %s", notification.Payload, practiceID)
		}
		count++
	}
	return count
}

func workspaceHints(notifications []*pgconn.Notification) int {
	count := 0
	for _, notification := range notifications {
		if notification.Channel == "acuity_workspace_hints" {
			count++
		}
	}
	return count
}
