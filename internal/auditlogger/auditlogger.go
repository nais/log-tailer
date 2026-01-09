package auditlogger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"cloud.google.com/go/logging"
	mrpb "google.golang.org/genproto/googleapis/api/monitoredres"
)

type AuditLogger struct {
	logEntries   <-chan map[string]interface{}
	clusterName  string
	projectID    string
	googleLogger *logging.Logger
	logger       *slog.Logger
}

func NewAuditLogger(logEntries <-chan map[string]interface{}, clusterName, projectID string, googleLoggingClient *logging.Client, logger *slog.Logger) *AuditLogger {
	return &AuditLogger{
		logEntries,
		clusterName,
		projectID,
		googleLoggingClient.Logger("postgres-audit-log"),
		logger.With(slog.Any("component", "auditLogger")),
	}
}

func (a *AuditLogger) Log(ctx context.Context) {
	a.logger.Info("Starting audit logger")
	for {
		select {
		case <-ctx.Done():
			a.logger.Info("Context cancelled, stopping processing")
			return
		case logEntry := <-a.logEntries:
			if err := a.sendToGCP(logEntry); err != nil {
				a.logger.Error("Error sending audit log to GCP", slog.Any("error", err))
			}
		}
	}
}

func (a *AuditLogger) sendToGCP(logEntry map[string]interface{}) error {
	entryJSON, err := json.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	// Extract additional fields for labels
	labels := make(map[string]string)

	// Add cluster name as database_id
	labels["databaseId"] = fmt.Sprintf("%s:%s", a.projectID, a.clusterName)

	// Extract user from root level
	if user, ok := logEntry["user"].(string); ok && user != "" {
		labels["user"] = user
	}

	// Extract dbname from root level
	if dbname, ok := logEntry["dbname"].(string); ok && dbname != "" {
		labels["databaseName"] = dbname
	}

	// Parse the AUDIT message to extract statement class
	// Format: "AUDIT: SESSION,15,1,READ,SELECT,,,..."
	// Fields: type, session_line, statement_id, class, command, ...
	if message, ok := logEntry["message"].(string); ok {
		// Split by comma after "AUDIT: "
		auditPrefix := "AUDIT: "
		if strings.HasPrefix(message, auditPrefix) {
			auditData := strings.TrimPrefix(message, auditPrefix)
			parts := strings.Split(auditData, ",")

			// Extract audit type (SESSION, OBJECT, etc.) - index 0
			if len(parts) > 0 && parts[0] != "" {
				labels["auditType"] = parts[0]
			}

			// Extract statement class (READ, WRITE, etc.) - index 3
			if len(parts) > 3 && parts[3] != "" {
				labels["auditClass"] = parts[3]
			}

			// Extract command (SELECT, INSERT, UPDATE, DELETE, etc.) - index 4
			if len(parts) > 4 && parts[4] != "" {
				labels["command"] = parts[4]
			}
		}
	}

	// Extract backend_type if present
	if backendType, ok := logEntry["backend_type"].(string); ok && backendType != "" {
		labels["backendType"] = backendType
	}

	// Create monitored resource with database_id and project_id
	resource := &mrpb.MonitoredResource{
		Type: "generic_node",
		Labels: map[string]string{
			"location":   "europe-north1",
			"namespace":  "postgres-audit",
			"node_id":    fmt.Sprintf("%s:%s", a.projectID, a.clusterName),
			"project_id": a.projectID,
		},
	}

	entry := logging.Entry{
		Payload:  string(entryJSON),
		Severity: logging.Info,
		Labels:   labels,
		Resource: resource,
	}

	a.googleLogger.Log(entry)

	// Flush after every entry, to ensure it is sent right away to avoid losing entries in the event of crash or unexpected exit
	if err = a.googleLogger.Flush(); err != nil {
		return fmt.Errorf("failed to flush logger: %w", err)
	}

	return nil
}
