package main

import (
	"context"
	"flag"
	"fmt"
	"log-tailer/internal/auditlogger"
	"log-tailer/internal/filelogger"
	"log-tailer/internal/tailer"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"cloud.google.com/go/logging"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	auditLogEntryCapacity = 100
	fileLogLinesCapacity  = 100
)

func main() {
	logFilePath := flag.String("log-file", "", "Glob pattern to the log file to tail (required)")
	projectID := flag.String("project-id", "", "GCP project ID (optional, for local testing)")

	flag.Parse()

	mainLogger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if *logFilePath == "" {
		flag.Usage()
		mainLogger.Error("Flag -log-file is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handleShutdown(cancel, mainLogger)

	var namespace string
	var clusterName string
	var teamProjectID string

	// If project-id is provided, use it for local testing
	if *projectID != "" {
		teamProjectID = *projectID
		namespace = "local"
		clusterName = "local-cluster"
		mainLogger.Info("Running in local mode", slog.Any("projectID", teamProjectID))
	} else {
		// Only create K8s client when running in cluster
		k8sClient, err := getK8sClient()
		if err != nil {
			mainLogger.Error("Failed to create Kubernetes client", slog.Any("error", err))
			os.Exit(2)
		}

		namespace, clusterName, err = getPodInfo(k8sClient)
		if err != nil {
			mainLogger.Error("Failed to get pod info", slog.Any("error", err))
			os.Exit(2)
		}

		teamProjectID, err = getProjectIDFromNamespace(k8sClient, namespace)
		if err != nil {
			mainLogger.Error("Failed to get project ID", slog.Any("error", err))
			os.Exit(2)
		}

		mainLogger.Info(fmt.Sprintf("Sending audit logs to project: %s in namespace: %s for cluster: %s", teamProjectID, namespace, clusterName))
	}

	mainLogger = mainLogger.With(slog.Any("projectID", teamProjectID), slog.Any("namespace", namespace), slog.Any("clusterName", clusterName))

	quit := make(chan error)
	logEntries := make(chan map[string]interface{}, auditLogEntryCapacity)
	logLines := make(chan string, fileLogLinesCapacity)

	fileLogger := filelogger.NewFileLogger(logLines, mainLogger)
	go fileLogger.Log(ctx)

	go tailer.Watch(ctx, *logFilePath, logEntries, logLines, quit, mainLogger.With(slog.Any("component", "tailer")))

	client, err := logging.NewClient(ctx, teamProjectID)
	if err != nil {
		mainLogger.Error("Failed to create logging client", slog.Any("error", err))
		os.Exit(4)
	}
	defer client.Close()

	auditLogger := auditlogger.NewAuditLogger(logEntries, clusterName, teamProjectID, client, mainLogger)
	go auditLogger.Log(ctx)

	select {
	case <-ctx.Done():
	case err = <-quit:
		mainLogger.Error("Error during processing of logs", slog.Any("error", err))
		os.Exit(100)
	}
}

func handleShutdown(cancel context.CancelFunc, logger *slog.Logger) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	logger.Info("Shutting down gracefully...")
	cancel()
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
