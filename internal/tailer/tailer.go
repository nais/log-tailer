package tailer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"strings"

	"github.com/SEKOIA-IO/tail"
	"github.com/fsnotify/fsnotify"
)

const (
	truncatedLength = 200
)

// Tailer tails a single log file and sends parsed log entries to channels
type Tailer struct {
	tail *tail.Tail

	filePath       string
	logEntries     chan<- map[string]any
	logLines       chan<- string
	internalLogger *slog.Logger
}

func Watch(ctx context.Context, logFilePattern string, logEntries chan<- map[string]any, logLines chan<- string, quit chan<- error, logger *slog.Logger) {
	tailers := make(map[string]*Tailer)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		quit <- fmt.Errorf("fsnotify.NewWatcher() failed: %w", err)
		return
	}
	defer watcher.Close()

	dir := path.Dir(logFilePattern)
	if err = watcher.Add(dir); err != nil {
		quit <- fmt.Errorf("error creating watch for directory(%q): %w", dir, err)
		return
	}

	if err := lookForFiles(ctx, logFilePattern, logEntries, logLines, logger, tailers); err != nil {
		quit <- err
		return
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, stopping processing")
			return
		case event := <-watcher.Events:
			if event.Has(fsnotify.Create) {
				logger.Debug("Fsnotify sent event", slog.Any("event", event))
				if err := lookForFiles(ctx, logFilePattern, logEntries, logLines, logger, tailers); err != nil {
					quit <- err
					return
				}
			}
		case err = <-watcher.Errors:
			logger.Error("Error watching files", slog.Any("error", err))
			quit <- err
			return
		}
	}
}

func lookForFiles(ctx context.Context, logFilePattern string, logEntries chan<- map[string]any, logLines chan<- string, logger *slog.Logger, tailers map[string]*Tailer) error {
	logger.Info("Looking for files matching pattern", slog.String("pattern", logFilePattern))
	matches, err := filepath.Glob(logFilePattern)
	if err != nil {
		logger.Error("Error listing files", slog.Any("error", err))
		return err
	}
	for _, match := range matches {
		if _, ok := tailers[match]; !ok {
			logger.Info("New file found, starting tail", slog.String("filepath", match))
			t, err := NewTailer(match, logEntries, logLines, logger)
			if err != nil {
				return err
			}
			tailers[match] = t
			go t.Tail(ctx)
		}
	}
	return nil
}

type tailLogger struct {
	logger *slog.Logger
}

func (l *tailLogger) Printf(format string, v ...any) {
	l.logger.Info(fmt.Sprintf(format, v...), slog.Any("component", "tailer-lib"))
}

func NewTailer(filePath string, logEntries chan<- map[string]any, logLines chan<- string, internalLogger *slog.Logger) (*Tailer, error) {
	tailer, err := tail.TailFile(filePath, tail.Config{Follow: true, ReOpen: true, CompleteLines: true, Logger: &tailLogger{internalLogger}})
	if err != nil {
		internalLogger.Error("Unable to tail file", slog.String("filepath", filePath), slog.Any("error", err))
		return nil, err
	}

	return &Tailer{
		tailer,
		filePath,
		logEntries,
		logLines,
		internalLogger.With(slog.String("filename", path.Base(filePath))),
	}, nil
}

func (t *Tailer) Tail(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			t.internalLogger.Info("Context cancelled, stopping tailing")
			t.tail.Cleanup()
			return
		case tailEntry, ok := <-t.tail.Lines:
			if !ok {
				t.internalLogger.Info("Tail channel closed, stopping tailing", slog.Any("error", t.tail.Err()))
				return
			}

			line := strings.TrimSpace(tailEntry.Text)

			if line == "" {
				continue // Skip empty lines
			}

			// Parse JSON log entry
			var logEntry map[string]any
			if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
				truncatedLine := line
				if len(truncatedLine) > truncatedLength {
					truncatedLine = truncatedLine[:truncatedLength]
				}
				t.internalLogger.Warn("Failed to parse JSON log line", slog.Any("error", err), slog.String("truncated_line", truncatedLine))
				continue
			}

			// Process the log entry
			if message, ok := logEntry["message"].(string); ok && strings.HasPrefix(message, "AUDIT:") {
				select {
				case t.logEntries <- logEntry:
				case <-ctx.Done():
				}
			} else {
				select {
				case t.logLines <- line:
				case <-ctx.Done():
				}
			}
		}
	}
}
