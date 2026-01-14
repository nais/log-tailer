package auditlogger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

type DryRunAuditLogger struct {
	logEntries <-chan map[string]any
	quit       chan<- error
	logger     *slog.Logger
}

func NewDryRunAuditLogger(logEntries <-chan map[string]any, quit chan<- error, logger *slog.Logger) *DryRunAuditLogger {
	return &DryRunAuditLogger{
		logEntries: logEntries,
		quit:       quit,
		logger:     logger.With(slog.String("component", "dryRunAuditLogger")),
	}
}

func (d *DryRunAuditLogger) Log(ctx context.Context) {
	d.logger.Info("Starting dry-run audit logger")
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Context cancelled, stopping processing")
			return
		case logEntry := <-d.logEntries:
			if err := d.printEntry(logEntry); err != nil {
				d.logger.Error("Error printing audit log", slog.Any("error", err))
				d.quit <- err
				return
			}
		}
	}
}

func (d *DryRunAuditLogger) printEntry(logEntry map[string]any) error {
	entryJSON, err := json.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	fmt.Printf("[DRY-RUN AUDIT] %s\n", string(entryJSON))
	return nil
}
