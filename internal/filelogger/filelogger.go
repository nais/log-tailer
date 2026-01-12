package filelogger

import (
	"context"
	"fmt"
	"log/slog"
)

type FileLogger struct {
	logLines <-chan string
	logger   *slog.Logger
}

func NewFileLogger(logLines <-chan string, logger *slog.Logger) *FileLogger {
	return &FileLogger{
		logLines,
		logger.With(slog.String("component", "fileLogger")),
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
			fmt.Println(logLine)
		}
	}
}
