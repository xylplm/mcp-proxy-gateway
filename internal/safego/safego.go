package safego

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

func LogRecovered(logger *slog.Logger, message string, recovered any, attrs ...any) {
	if logger == nil {
		logger = slog.Default()
	}
	attrs = append(attrs,
		"panic", fmt.Sprint(recovered),
		"stack", string(debug.Stack()),
	)
	logger.Error(message, attrs...)
}
