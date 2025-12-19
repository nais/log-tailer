package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/logging"
	mrpb "google.golang.org/genproto/googleapis/api/monitoredres"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	logFilePath := flag.String("log-file", "", "Path to the log file to tail (required)")
	projectID := flag.String("project-id", "", "GCP project ID (optional, for local testing")

	flag.Parse()

	if *logFilePath == "" {
		flag.Usage()
		log.Fatal("Flag -log-file is required")
	}

	logFile, err := os.Open(*logFilePath)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handleShutdown(cancel)

	var namespace string
	var clusterName string
	var teamProjectID string

	k8sClient, err := getK8sClient()
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// If project-id is provided, use it for local testing
	if *projectID != "" {
		teamProjectID = *projectID
		namespace = "local"
		clusterName = "local-cluster"
		log.Printf("Running in local mode with project: %s", teamProjectID)
	} else {
		namespace, clusterName, err = getPodInfo(k8sClient)
		if err != nil {
			log.Fatalf("Failed to get pod info: %v", err)
		}

		teamProjectID, err = getProjectIDFromNamespace(k8sClient, namespace)
		if err != nil {
			log.Fatalf("Failed to get project ID: %v", err)
		}

		log.Printf("Sending audit logs to project: %s in namespace: %s for cluster: %s", teamProjectID, namespace, clusterName)
	}

	client, err := logging.NewClient(ctx, teamProjectID)
	if err != nil {
		log.Fatalf("Failed to create logging client: %v", err)
	}
	defer client.Close()

	// Seek to end of file if it exists (don't reprocess old logs on restart)
	if info, err := logFile.Stat(); err == nil && info.Size() > 0 {
		if _, err := logFile.Seek(0, 2); err != nil {
			log.Printf("Warning: Failed to seek to end of file: %v", err)
		} else {
			log.Printf("Seeking to end of existing log file (size: %d bytes)", info.Size())
		}
	}

	// Track file info for rotation detection
	var lastFileInfo os.FileInfo
	if info, err := logFile.Stat(); err == nil {
		lastFileInfo = info
	}

	// Use the log file as the input source
	decoder := json.NewDecoder(logFile)
	log.Println("Starting log tail...")

	// Ticker to check for log rotation every 5 seconds
	rotationCheckTicker := time.NewTicker(5 * time.Second)
	defer rotationCheckTicker.Stop()

	for {
		var logEntry map[string]interface{}
		if err := decoder.Decode(&logEntry); err != nil {
			if err.Error() == "EOF" {
				// Reached end of file, wait a bit and continue (tail behavior)
				select {
				case <-ctx.Done():
					log.Println("Context cancelled, stopping log processing")
					return
				case <-rotationCheckTicker.C:
					// Check if log file has been rotated
					if rotated, err := checkLogRotation(*logFilePath, lastFileInfo); err != nil {
						log.Printf("Error checking log rotation: %v", err)
					} else if rotated {
						log.Println("Log rotation detected, reopening file...")
						if err := logFile.Close(); err != nil {
							log.Printf("Warning: Failed to close old log file: %v", err)
						}

						// Reopen the file
						newFile, err := os.Open(*logFilePath)
						if err != nil {
							log.Printf("Failed to reopen log file after rotation: %v", err)
							continue
						}

						logFile = newFile
						decoder = json.NewDecoder(logFile)

						// Update file info
						if info, err := logFile.Stat(); err == nil {
							lastFileInfo = info
							log.Printf("Successfully reopened log file (new size: %d bytes)", info.Size())
						}
					}
				case <-time.After(100 * time.Millisecond):
					// Just wait and retry
				}
				continue
			}
			log.Printf("Failed to read log entry: %v", err)
			continue
		}

		// Check for context cancellation between processing entries
		select {
		case <-ctx.Done():
			log.Println("Context cancelled, stopping log processing")
			return
		default:
		}

		// Process the log entry
		if message, ok := logEntry["message"].(string); ok && strings.HasPrefix(message, "AUDIT:") {
			// Send to GCP in background to avoid blocking
			go func(entry map[string]interface{}) {
				if err := sendToGCP(client, entry, clusterName, teamProjectID); err != nil {
					log.Printf("Failed to send audit log: %v", err)
				}
			}(logEntry)
		} else {
			// Non-audit logs printed to stdout
			if jsonOutput, err := json.Marshal(logEntry); err == nil {
				fmt.Println(string(jsonOutput))
			}
		}
	}
}

func sendToGCP(client *logging.Client, logEntry map[string]interface{}, clusterName, projectID string) error {
	entryJSON, err := json.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	logger := client.Logger("postgres-audit-log")

	// Extract additional fields for labels
	labels := make(map[string]string)

	// Add cluster name as database_id
	labels["databaseId"] = fmt.Sprintf("%s:%s", projectID, clusterName)

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
			"node_id":    fmt.Sprintf("%s:%s", projectID, clusterName),
			"project_id": projectID,
		},
	}

	entry := logging.Entry{
		Payload:  string(entryJSON),
		Severity: logging.Info,
		Labels:   labels,
		Resource: resource,
	}

	logger.Log(entry)

	if err := logger.Flush(); err != nil {
		return fmt.Errorf("failed to flush logger: %w", err)
	}

	return nil
}

func handleShutdown(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	log.Println("Shutting down gracefully...")
	cancel()
}

// checkLogRotation detects if the log file has been rotated
// by comparing file stats (inode on Unix or size decrease)
func checkLogRotation(filePath string, lastInfo os.FileInfo) (bool, error) {
	if lastInfo == nil {
		return false, nil
	}

	currentInfo, err := os.Stat(filePath)
	if err != nil {
		// File doesn't exist, might have been rotated and new one not created yet
		return true, nil
	}

	// Check if it's a different file (different inode on Unix systems)
	if !os.SameFile(lastInfo, currentInfo) {
		return true, nil
	}

	// Check if file size decreased (indicates rotation/truncation)
	if currentInfo.Size() < lastInfo.Size() {
		return true, nil
	}

	return false, nil
}

func getPodInfo(client *kubernetes.Clientset) (namespace, clusterName string, err error) {
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return "", "", fmt.Errorf("POD_NAME environment variable is not set")
	}

	namespace = os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		return "", "", fmt.Errorf("POD_NAMESPACE environment variable is not set")
	}

	// Get pod to extract cluster name from labels
	pod, err := client.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to get pod: %w", err)
	}

	clusterName, ok := pod.Labels["cluster-name"]
	if !ok {
		return "", "", fmt.Errorf("cluster-name label not found in pod metadata")
	}

	return namespace, clusterName, nil
}

func getProjectIDFromNamespace(client *kubernetes.Clientset, namespace string) (string, error) {
	ns, err := client.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}

	projectID, ok := ns.Labels["google-cloud-project"]
	if !ok {
		return "", fmt.Errorf("google-cloud-project label not found in namespace %s", namespace)
	}

	return projectID, nil
}

func getK8sClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return clientset, nil
}
