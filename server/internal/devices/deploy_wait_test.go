package devices

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWaitDeployCancellationLeavesOutcomeUnconfirmed(t *testing.T) {
	m := &Machine{deploying: true, deployDone: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := m.waitDeploy(ctx)
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("waitDeploy error = %v, want an unconfirmed running deployment", err)
	}
}
