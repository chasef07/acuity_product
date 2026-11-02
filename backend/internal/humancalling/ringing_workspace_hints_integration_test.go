package humancalling_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestRingingInboundStaffLegsLeaveTheWorkspaceUnchanged(t *testing.T) {
	call := newInboundCall(t, "ringing-workspace")
	reads := workspace.New(call.db.As(testdb.Portal), access.New(call.db.As(testdb.Portal), nil))
	timeline := func() workspace.TimelinePage {
		t.Helper()
		page, err := reads.QueryPhoneTimeline(context.Background(), workspace.QueryPhoneTimelineCommand{
			Identity: call.staff[0], PracticeID: call.authorization.Practice.ID, Phone: "+15555550100",
		})
		if err != nil {
			t.Fatalf("read phone Engagement History: %v", err)
		}
		return page
	}
	listener := listenForHints(t, call.db.Owner)
	for _, step := range call.steps()[:7] {
		drainHints(t, listener)
		before := timeline()
		step.run(t)
		hints := workspaceHints(drainHints(t, listener))
		changed := !reflect.DeepEqual(before, timeline())
		switch {
		case strings.HasSuffix(step.name, "ringing"):
			if hints != 0 || changed {
				t.Errorf("%s: %d workspace hints, Engagement History changed %t, want neither", step.name, hints, changed)
			}
		case step.name == "Staff 1 answered":
			if hints != 1 || !changed {
				t.Errorf("%s: %d workspace hints, Engagement History changed %t, want one hint for the change",
					step.name, hints, changed)
			}
		}
	}
}
