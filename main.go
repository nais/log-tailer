package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"log-tailer/internal/auditlogger"
	"log-tailer/internal/filelogger"
	"log-tailer/internal/tailer"

	"cloud.google.com/go/logging"
	"github.com/spf13/cobra"
	"google.golang.org/api/option"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	auditLogEntryCapacity = 100
	fileLogLinesCapacity  = 100
)

func main() {
	var projectID string

	var rootCmd = &cobra.Command{
		Use:   "log-tailer <log-file-pattern>",
		Short: "log-tailer tails JSON logs from Postgres, and ships audit-logs to a Google logging sink.",
		Long: `Log-tailer tails JSON logs from Postgres and ships audit-logs to a Google 
logging sink based on the project annotation on the namespace it is running in.
Non audit-log messages are printed unmodified to stdout.
The tool itself might emit logging on stderr.

Arguments:
  log-file-pattern		Glob-pattern for the files to tail
		`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			mainFunc(args[0], projectID)
		},
	}
	rootCmd.Flags().StringVar(&projectID, "project-id", "", "GCP project ID (optional, for local testing)")

	err := rootCmd.Execute()
	if err != nil {
		os.Exit(101)
	}
}

func mainFunc(logFilePath, projectID string) {
	mainLogger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handleShutdown(cancel, mainLogger)

	var namespace string
	var clusterName string
	var teamProjectID string

	// If project-id is provided, use it for local testing
	if projectID != "" {
		teamProjectID = projectID
		namespace = "local"
		clusterName = "local-cluster"
		mainLogger.Info("Running in local mode", slog.String("projectID", teamProjectID))
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
	}

	mainLogger = mainLogger.With(slog.String("projectID", teamProjectID), slog.String("namespace", namespace), slog.String("clusterName", clusterName))
	mainLogger.Info("Sending audit logs to project")

	quit := make(chan error)
	logEntries := make(chan map[string]interface{}, auditLogEntryCapacity)
	logLines := make(chan string, fileLogLinesCapacity)

	fileLogger := filelogger.NewFileLogger(logLines, mainLogger)
	go fileLogger.Log(ctx)

	go tailer.Watch(ctx, logFilePath, logEntries, logLines, quit, mainLogger.With(slog.String("component", "tailer")))

	googleLoggingClientLogger := mainLogger.With(slog.String("component", "google-logging-client"))
	client, err := logging.NewClient(ctx, teamProjectID, option.WithLogger(googleLoggingClientLogger))
	if err != nil {
		mainLogger.Error("Failed to create logging client", slog.Any("error", err))
		os.Exit(4)
	}
	defer client.Close()

	err = client.Ping(ctx)
	if err != nil {
		mainLogger.Error("Failed to ping google logging service", slog.Any("error", err))
		os.Exit(5)
	}

	auditLogger := auditlogger.NewAuditLogger(logEntries, quit, clusterName, teamProjectID, client, mainLogger)
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
