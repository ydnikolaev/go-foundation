package alert

import (
	"context"
	"log/slog"
)

// Log is the notifier of dev and tests: it writes the alert as one Info line instead of sending it.
// Info, not Error, so the line is never fed back through Handler.
type Log struct{ log *slog.Logger }

// NewLog returns a Log notifier writing to l (the base logger).
func NewLog(l *slog.Logger) *Log { return &Log{log: l} }

// Notify implements Notifier.
func (n *Log) Notify(ctx context.Context, ev Event) error {
	n.log.InfoContext(ctx, "alert", "alert_kind", ev.Kind, "text", ev.Text())
	return nil
}
