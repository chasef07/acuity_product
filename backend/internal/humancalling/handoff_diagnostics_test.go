package humancalling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/jackc/pgx/v5"
)

func TestHandoffRejectionsExplainAdmissionWithoutIdentifiers(t *testing.T) {
	for _, reason := range []string{"provider_identity", "connection", "address", "missing_handoff", "ambiguous_handoff"} {
		t.Run(reason, func(t *testing.T) {
			var output bytes.Buffer
			m := &Module{config: Config{CallControlID: "expected-connection"}, observer: observability.NewLogger(observability.RuntimeWorker, "synthetic-revision", slog.New(slog.NewJSONHandler(&output, nil)))}
			fact := ProviderFact{CallControlID: "synthetic-control", CallLegID: "synthetic-leg", CallSessionID: "synthetic-session", ConnectionID: "expected-connection", From: "+15555550100", To: "+15555550101"}
			var err error
			switch reason {
			case "provider_identity":
				fact.CallControlID = ""
				err = m.admitHandoff(context.Background(), fact)
			case "connection":
				fact.ConnectionID = "unrelated-connection"
				err = m.admitHandoff(context.Background(), fact)
			default:
				candidates := 0
				if reason == "ambiguous_handoff" {
					candidates = 2
				}
				if reason == "address" {
					fact.From = "synthetic-private-address"
				}
				_, _, _, err = m.resolveHandoffForRefer(context.Background(), handoffDiagnosticTx{candidates: candidates}, fact)
			}
			if !errors.Is(err, ErrInvalidHandoff) {
				t.Fatalf("admission error = %v", err)
			}
			var entry map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &entry); err != nil {
				t.Fatalf("missing rejection diagnostic: %v", err)
			}
			if entry["metric"] != "acuity_call_center_handoff_rejection" || entry["reason"] != reason {
				t.Fatalf("rejection diagnostic = %#v", entry)
			}
			for _, forbidden := range []string{"synthetic-control", "synthetic-leg", "synthetic-session", "unrelated-connection", "synthetic-private-address", "+15555550100"} {
				if strings.Contains(output.String(), forbidden) {
					t.Fatalf("diagnostic exposes identifier %q", forbidden)
				}
			}
		})
	}
}

type handoffDiagnosticTx struct {
	pgx.Tx
	candidates int
}

func (tx handoffDiagnosticTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return &handoffDiagnosticRows{remaining: tx.candidates}, nil
}

type handoffDiagnosticRows struct {
	pgx.Rows
	remaining int
}

func (r *handoffDiagnosticRows) Next() bool {
	if r.remaining == 0 {
		return false
	}
	r.remaining--
	return true
}
func (*handoffDiagnosticRows) Scan(dest ...any) error {
	for _, d := range dest {
		*d.(*string) = "synthetic-id"
	}
	return nil
}
func (*handoffDiagnosticRows) Err() error { return nil }
func (*handoffDiagnosticRows) Close()     {}
