package aggregation

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func TestSessionInvokerConcurrentCallsKeepResponsesIsolated(t *testing.T) {
	session := &siEchoSession{}
	invoker := NewSessionInvoker(
		&siFakeStates{state: domain.ConnAvailable},
		&siFakeSessions{session: session, ok: true},
		time.Second,
		nil,
	)

	const workers = 128
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			args := json.RawMessage(fmt.Sprintf(`{"seq":%d,"payload":"call-%d"}`, i, i))
			got, err := invoker.CallUpstream(context.Background(), "up-a", "echo", args)
			if err != nil {
				errCh <- fmt.Errorf("call %d returned error: %w", i, err)
				return
			}
			if string(got.Content) != string(args) {
				errCh <- fmt.Errorf("call %d response crossed data boundary: got %s want %s", i, got.Content, args)
				return
			}
			errCh <- nil
		}()
	}

	for i := 0; i < workers; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	if got := session.calls.Load(); got != workers {
		t.Fatalf("all concurrent calls should reach upstream once, got %d want %d", got, workers)
	}
}
