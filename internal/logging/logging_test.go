package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerRedactsSensitiveAttributes(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := NewJSON(&output, slog.LevelInfo).With("site_cookie", "session-value")
	logger.Info("request", "authorization", "Bearer value", "site", "example")

	logged := output.String()
	if strings.Contains(logged, "session-value") || strings.Contains(logged, "Bearer value") {
		t.Fatalf("sensitive value leaked: %s", logged)
	}
	if !strings.Contains(logged, "example") || strings.Count(logged, "[REDACTED]") != 2 {
		t.Fatalf("unexpected log output: %s", logged)
	}
}
