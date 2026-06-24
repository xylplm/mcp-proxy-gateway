package app

import (
	"log/slog"
	"testing"
)

func TestRunBackgroundStepRecoversAndAllowsNextStep(t *testing.T) {
	a := &App{logger: slog.Default()}

	a.runBackgroundStep("panic-step", func() {
		panic("boom")
	})

	ran := false
	a.runBackgroundStep("next-step", func() {
		ran = true
	})
	if !ran {
		t.Fatal("panic in one background startup step should not prevent later steps")
	}
}
