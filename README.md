# Log Tailer

A Go application that tails PostgreSQL JSON logs and routes audit logs to GCP Cloud Logging while streaming other logs to stdout.

## Features

- Tails JSON-formatted log files
- Detects audit logs (messages starting with "AUDIT:") and sends them to GCP Cloud Logging
- Streams non-audit logs to stdout
- Automatically retrieves GCP project ID from parent namespace annotation
- Sends logs to default log bucket (routing via log sinks can be configured in GCP)
- Graceful shutdown handling

## Usage

```bash
log-tailer -log-file <path-to-log-file>
```

### Flags

- `-log-file`: Path to the log file to tail (required)

### Example

```bash
log-tailer -log-file /home/postgres/pgdata/pgroot/pg_log/postgresql.json
```

## How It Works

The application automatically determines the GCP project ID by:
1. Reading the pod's namespace (e.g., `pg-example`)
2. Extracting the parent namespace name (e.g., `example`)
3. Reading the `cnrm.cloud.google.com/project-id` annotation from the parent namespace
4. Sending audit logs to the default log bucket in that project

Log sinks can be configured in GCP to route logs to specific buckets based on filters.

## Kubernetes Deployment

The application requires:
- **Environment variables**:
  - `POD_NAME`: Name of the pod
  - `POD_NAMESPACE`: Namespace of the pod (must follow `pg-*` format)

- **Parent namespace**:
  - Must have annotation `cnrm.cloud.google.com/project-id` with the GCP project ID

## Development

Uses [mise](https://mise.jdx.dev/) for tooling:

```bash
mise install      # Install dependencies
mise build        # Build the application
mise test         # Run tests
mise check        # Run all checks (vet, staticcheck, gosec, gofumpt, vulncheck)
```
