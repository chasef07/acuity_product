package interaction

import (
	"context"
	"testing"
)

func drainAnalyticsFacts(t *testing.T, module *Module) {
	t.Helper()
	for {
		projected, err := module.ProjectNextAnalyticsFacts(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !projected {
			return
		}
	}
}
