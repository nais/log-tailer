package tailer

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	retryInterval        = 5 * time.Second
	readInterval         = 100 * time.Millisecond
	truncatedLength      = 200
	newFileCheckInterval = 1 * time.Minute
)

type Tailer struct {
	filePath       string
	logEntries     chan<- map[string]interface{}
	logLines       chan<- string
	internalLogger *slog.Logger
}

func Watch(ctx context.Context, logFilePattern string, logEntries chan<- map[string]interface{}, logLines chan<- string, quit chan<- error, logger *slog.Logger) {
	tailers := make(map[string]*Tailer)
	err := lookForFiles(ctx, logFilePattern, logEntries, logLines, logger, tailers)
	if err != nil {
		quit <- err
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Error("Unable to use fsnotify to watch for file changes, falling back to ticker")
		newFileCheckTicker := time.NewTicker(newFileCheckInterval)
		defer newFileCheckTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.Info("Context cancelled, stopping processing")
				return
			case <-newFileCheckTicker.C:
				logger.Info("Ticker ticked")
				err = lookForFiles(ctx, logFilePattern, logEntries, logLines, logger, tailers)
				if err != nil {
					quit <- err
					return
				}
			}
		}
	} else {
		dir := path.Dir(logFilePattern)
		if err = watcher.Add(dir); err != nil {
			logger.Error("Error creating watch for directory", slog.Any("error", err), slog.String("directory", dir))
		}
		defer watcher.Close()

		for {
			select {
			case <-ctx.Done():
				logger.Info("Context cancelled, stopping processing")
				return
			case event := <-watcher.Events:
				if event.Has(fsnotify.Create) {
					logger.Debug("Fsnotify sent event", slog.Any("event", event))
					err = lookForFiles(ctx, logFilePattern, logEntries, logLines, logger, tailers)
					if err != nil {
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
}

func lookForFiles(ctx context.Context, logFilePattern string, logEntries chan<- map[string]interface{}, logLines chan<- string, logger *slog.Logger, tailers map[string]*Tailer) error {
	logger.Info("Looking for files matching pattern", slog.String("pattern", logFilePattern))
	matches, err := filepath.Glob(logFilePattern)
	if err != nil {
		logger.Error("Error listing files", slog.Any("error", err))
		return err
	}
	for _, match := range matches {
		if _, ok := tailers[match]; !ok {
			logger.Info("New file found, starting tail", slog.String("filepath", match))
			t := NewTailer(match, logEntries, logLines, logger)
			tailers[match] = t
			go t.Tail(ctx)
		}
	}
	return nil
}

func NewTailer(filePath string, logEntries chan<- map[string]interface{}, logLines chan<- string, internalLogger *slog.Logger) *Tailer {
	return &Tailer{
		filePath,
		logEntries,
		logLines,
		internalLogger.With(slog.String("filename", path.Base(filePath))),
	}
}

func (t *Tailer) Tail(ctx context.Context) {
	var err error
	var logFile *os.File
	for {
		logFile, err = os.Open(t.filePath)
		if err != nil {
			t.internalLogger.Error("Unable to open file, retrying", slog.Any("error", err), slog.Any("retryInterval", retryInterval))
			time.Sleep(retryInterval)
			continue
		}
		break
	}
	defer logFile.Close()

	// Seek to end of file if it exists (don't reprocess old logs on restart)
	// Track file info for rotation detection
	var lastFileInfo os.FileInfo
	if info, err := logFile.Stat(); err == nil {
		lastFileInfo = info
		if info.Size() > 0 {
			if pos, err := logFile.Seek(0, 2); err != nil {
				t.internalLogger.Warn("Failed to seek to end of file", slog.Any("error", err))
			} else {
				t.internalLogger.Info("Skipping existing log content - only new logs will be processed", slog.Int64("file_size_bytes", info.Size()), slog.Int64("position", pos))
			}
		} else {
			t.internalLogger.Info("Log file is empty - waiting for new log entries")
		}
	} else {
		t.internalLogger.Warn("Failed to stat log file", slog.Any("error", err))
	}

	// Use bufio.Reader for line-by-line reading with better tail support
	reader := bufio.NewReader(logFile)

	// Log the initial file position
	pos, _ := logFile.Seek(0, 1)
	t.internalLogger.Info("Start tailing file", slog.Int64("position", pos))

	// Ticker to check for log rotation every 5 seconds
	rotationCheckTicker := time.NewTicker(5 * time.Second)
	defer rotationCheckTicker.Stop()

	entriesProcessed := 0

	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			t.internalLogger.Info("Context cancelled, stopping log processing")
			return
		default:
		}

		// Non-blocking rotation check
		select {
		case <-rotationCheckTicker.C:
			if checkLogRotation(t.filePath, lastFileInfo) {
				t.internalLogger.Info("Log rotation detected, reopening file...")
				if err = logFile.Close(); err != nil {
					t.internalLogger.Warn("Failed to close old log file", slog.Any("error", err))
				}

				// Reopen the file
				newFile, err := os.Open(t.filePath)
				if err != nil {
					t.internalLogger.Warn("Failed to reopen log file after rotation", slog.Any("error", err), slog.Any("retryInterval", retryInterval))
					time.Sleep(retryInterval)
					continue
				}

				logFile = newFile
				reader = bufio.NewReader(logFile)

				// Update file info
				if info, err := logFile.Stat(); err == nil {
					lastFileInfo = info
					t.internalLogger.Info("Successfully reopened log file", slog.Int64("new_file_size_bytes", info.Size()))
				}
			}
		default:
			// Don't block on rotation check
		}

		// Try to read the next line
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// No more data available right now - wait and retry
				time.Sleep(readInterval)
				continue
			}

			// Other error
			t.internalLogger.Warn("Read error", slog.Any("error", err))
			time.Sleep(readInterval)
			continue
		}

		// Successfully read a line
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r") // Handle CRLF

		if line == "" {
			continue // Skip empty lines
		}

		// Parse JSON log entry
		var logEntry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &logEntry); err != nil {
			truncatedLine := line
			if len(truncatedLine) > truncatedLength {
				truncatedLine = truncatedLine[:truncatedLength]
			}
			t.internalLogger.Warn("Failed to parse JSON log line", slog.Any("error", err), slog.String("truncated_line", truncatedLine))
			continue
		}

		entriesProcessed++
		if entriesProcessed == 1 {
			t.internalLogger.Debug("Successfully read first log entry!")
		}
		if entriesProcessed%100 == 0 {
			t.internalLogger.Debug("Processing ...", slog.Int("entriesProcessed", entriesProcessed))
		}

		// Check for context cancellation between processing entries
		select {
		case <-ctx.Done():
			t.internalLogger.Info("Context cancelled, stopping log processing")
			return
		default:
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

// checkLogRotation detects if the log file has been rotated
// by comparing file stats (inode on Unix or size decrease)
func checkLogRotation(filePath string, lastInfo os.FileInfo) bool {
	if lastInfo == nil {
		return false
	}

	currentInfo, err := os.Stat(filePath)
	if err != nil {
		// File doesn't exist, might have been rotated and new one not created yet
		return true
	}

	// Check if it's a different file (different inode on Unix systems)
	if !os.SameFile(lastInfo, currentInfo) {
		return true
	}

	// Check if file size decreased (indicates rotation/truncation)
	if currentInfo.Size() < lastInfo.Size() {
		return true
	}

	return false
}
