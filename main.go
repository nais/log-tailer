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

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"cloud.google.com/go/logging"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	logFilePath := flag.String("log-file", "", "Path to the log file to tail (required)")

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

	namespace, err := getPodInfo()
	if err != nil {
		log.Fatalf("Failed to get pod info: %v", err)
	}

	projectID, err := getProjectIDFromParentNamespace(namespace)
	if err != nil {
		log.Fatalf("Failed to get project ID: %v", err)
	}

	log.Printf("Sending audit logs to project: %s in namespace: %s", projectID, namespace)

	client, err := logging.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to create logging client: %v", err)
	}
	defer client.Close()

	// Use the log file as the input source
	decoder := json.NewDecoder(logFile)
	for {
		var logEntry map[string]interface{}
		if err := decoder.Decode(&logEntry); err != nil {
			if err.Error() == "EOF" {
				break // End of file reached
			}
			log.Printf("Failed to read log entry: %v", err)
			continue
		}

		// Process the log entry
		if message, ok := logEntry["message"].(string); ok && strings.HasPrefix(message, "AUDIT:") {
			if err := sendToGCP(client, logEntry); err != nil {
				log.Printf("Failed to send audit log: %v", err)
			}
		} else {
			fmt.Println(logEntry)
		}
	}
}

func sendToGCP(client *logging.Client, logEntry map[string]interface{}) error {
	entryJSON, err := json.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	logger := client.Logger("audit-log")

	logger.Log(logging.Entry{
		Payload:  string(entryJSON),
		Severity: logging.Info,
	})

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

func getPodInfo() (namespace string, err error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return "", fmt.Errorf("failed to create in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	podName := os.Getenv("POD_NAME")
	if podName == "" {
		return "", fmt.Errorf("POD_NAME environment variable is not set")
	}

	namespace = os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		return "", fmt.Errorf("POD_NAMESPACE environment variable is not set")
	}

	// Verify pod exists
	_, err = clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get pod: %w", err)
	}

	return namespace, nil
}

func getProjectIDFromParentNamespace(namespace string) (string, error) {
	// We need to get the project ID from the actual team namespace
	parentNamespace := strings.TrimPrefix(namespace, "pg-")
	if parentNamespace == namespace {
		return "", fmt.Errorf("namespace %s does not follow expected pg-* format", namespace)
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return "", fmt.Errorf("failed to create in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Get the parent namespace to retrieve the project ID annotation
	ns, err := clientset.CoreV1().Namespaces().Get(context.TODO(), parentNamespace, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get parent namespace %s: %w", parentNamespace, err)
	}

	projectID, ok := ns.Annotations["cnrm.cloud.google.com/project-id"]
	if !ok {
		return "", fmt.Errorf("cnrm.cloud.google.com/project-id annotation not found in namespace %s", parentNamespace)
	}

	return projectID, nil
}
