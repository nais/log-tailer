package filelogger

import (
	"context"
	"fmt"
	"log/slog"
)

type FileLogger struct {
	logLines <-chan string
	logger   *slog.Logger
	dryRun   bool
}

func NewFileLogger(logLines <-chan string, logger *slog.Logger, dryRun bool) *FileLogger {
	return &FileLogger{
		logLines: logLines,
		logger:   logger.With(slog.String("component", "fileLogger")),
		dryRun:   dryRun,
	}
}

func (f *FileLogger) Log(ctx context.Context) {
	f.logger.Info("Starting file logging")
	for {
		select {
		case <-ctx.Done():
			f.logger.Info("Context cancelled, stopping processing")
			return
		case logLine := <-f.logLines:
			if f.dryRun {
				fmt.Printf("[DRY-RUN STDOUT] %s\n", logLine)
			} else {
				fmt.Println(logLine)
			}
		}
	}
}
