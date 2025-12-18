# Log Tailer

A Go application that tails PostgreSQL JSON logs and routes audit logs to GCP Cloud Logging while streaming other logs to stdout.

**Built with Chainguard Images** - Uses minimal, secure, and regularly updated base images from [Chainguard](https://www.chainguard.dev/) for enhanced security and reduced attack surface.

## Features

- Tails JSON-formatted log files continuously
- Detects audit logs (messages starting with "AUDIT:") and sends them to GCP Cloud Logging
- Streams non-audit logs to stdout
- Automatically retrieves GCP project ID from parent namespace annotation
- Extracts cluster name from pod metadata and adds it as database label in GCP logs
- Extracts additional fields (statement_class, user, table) from log entries as labels
- **Handles log rotation automatically** - detects when PostgreSQL rotates logs and reopens the file
- Sends logs to default log bucket (routing via log sinks can be configured in GCP)
- Graceful shutdown handling
- Seeks to end of file on startup to avoid reprocessing old logs

## Usage

### In Kubernetes (Production)

```bash
log-tailer -log-file <path-to-log-file>
```

The application will automatically fetch the GCP project ID from the parent namespace annotation.

### Local Testing

```bash
log-tailer -log-file <path-to-log-file> -project-id <gcp-project-id>
```

When `-project-id` is provided, the application runs in local mode and skips Kubernetes API calls.

### Flags

- `-log-file`: Path to the log file to tail (required)
- `-project-id`: GCP project ID (optional, for local testing; if not set, will be fetched from parent namespace)

### Examples

**Kubernetes mode:**
```bash
log-tailer -log-file /home/postgres/pgdata/pgroot/pg_log/postgresql.json
```

**Local mode:**
```bash
log-tailer -log-file /var/log/postgresql.json -project-id my-gcp-project
```

## How It Works

The application automatically determines the GCP project ID by:
1. Reading the pod's namespace (e.g., `pg-example`)
2. Extracting the parent namespace name (e.g., `example`)
3. Reading the `cnrm.cloud.google.com/project-id` annotation from the parent namespace
4. Sending audit logs to the default log bucket in that project

Log sinks can be configured in GCP to route logs to specific buckets based on filters.

### Log Rotation Handling

The application handles PostgreSQL log rotation automatically:
- **Every 5 seconds**, it checks if the log file has been rotated
- **Detection methods**:
  - Checks if the file inode has changed (different file)
  - Checks if the file size has decreased (truncation)
- **On rotation detection**:
  - Closes the old file handle
  - Opens the new log file
  - Continues tailing from the beginning of the new file

This ensures the sidecar continues to work indefinitely without pod restarts, even as PostgreSQL rotates logs (typically daily or when size limits are reached).

## Kubernetes Deployment

The application requires:
- **Environment variables**:
  - `POD_NAME`: Name of the pod
  - `POD_NAMESPACE`: Namespace of the pod (must follow `pg-*` format)

- **Pod labels**:
  - `cluster-name`: Name of the PostgreSQL cluster (used as database name in GCP logs)

- **Parent namespace**:
  - Must have annotation `cnrm.cloud.google.com/project-id` with the GCP project ID

### Log Fields Sent to GCP

Audit logs are sent to GCP Cloud Logging with the following structure:

**Resource Labels** (resource.labels):
- `database_id`: Set to the cluster name from pod label `cluster-name`
- `project_id`: Set to the GCP project ID
- `location`: "global"
- `namespace`: "postgres-audit"
- `node_id`: Set to the cluster name

**Entry Labels** (labels) - Extracted from log entry:
- `database_id`: Cluster name from pod label `cluster-name`
- `user`: Database user from root-level `user` field
- `dbname`: Database name from root-level `dbname` field
- `audit_type`: Audit type (SESSION, OBJECT, etc.) parsed from message
- `statement_class`: Statement class (READ, WRITE, etc.) parsed from message
- `command`: SQL command (SELECT, INSERT, UPDATE, DELETE, etc.) parsed from message
- `backend_type`: Backend type (client backend, etc.) from root-level field

**Payload**: The full log entry JSON is sent as the payload.

**Resource Type**: `generic_node`

#### PostgreSQL Audit Log Format

The application parses PostgreSQL audit logs with the following format:
```
AUDIT: {type},{session_line},{statement_id},{class},{command},{object_type},{object_name},{query},{params}
```

Example:
```
AUDIT: SESSION,15,1,READ,SELECT,,,SELECT pg_database_size($1),<not logged>
```

Extracted fields:
- Index 0: `audit_type` (SESSION, OBJECT, etc.)
- Index 3: `statement_class` (READ, WRITE, etc.)
- Index 4: `command` (SELECT, INSERT, UPDATE, DELETE, etc.)

## Local Testing

For local development and testing, you can run the application with Docker or nerdctl by providing the project ID explicitly:

### Quick Start

Use the included test script:

```bash
./test-local.sh your-gcp-project-id
```

This will build the image and run it with the included `test-logs.json` file.

### Using Docker

```bash
# Build the image
docker build -t log-tailer:local .

# Run with project ID for local testing
docker run --rm \
  -v ~/.config/gcloud/application_default_credentials.json:/gcp/credentials.json:ro \
  -e GOOGLE_APPLICATION_CREDENTIALS=/gcp/credentials.json \
  -v /path/to/log-file.json:/logs/postgresql.json:ro \
  log-tailer:local \
  -log-file /logs/postgresql.json \
  -project-id your-gcp-project-id
```

### Using nerdctl

```bash
# Build the image
nerdctl build -t log-tailer:local .

# Run with project ID for local testing
nerdctl run --rm \
  -v ~/.config/gcloud/application_default_credentials.json:/gcp/credentials.json:ro \
  -e GOOGLE_APPLICATION_CREDENTIALS=/gcp/credentials.json \
  -v /path/to/log-file.json:/logs/postgresql.json:ro \
  log-tailer:local \
  -log-file /logs/postgresql.json \
  -project-id your-gcp-project-id
```

### Example with Test Log File

```bash
# Using the included test log file
docker run --rm \
  -v ~/.config/gcloud/application_default_credentials.json:/gcp/credentials.json:ro \
  -e GOOGLE_APPLICATION_CREDENTIALS=/gcp/credentials.json \
  -v $(pwd)/test-logs.json:/logs/test.json:ro \
  log-tailer:local \
  -log-file /logs/test.json \
  -project-id my-test-project
```

**Note**: When running locally with `-project-id`, the application will not attempt to connect to the Kubernetes API, making it suitable for testing without cluster access. You'll need to mount your GCP credentials file as shown above.

## Development

Uses [mise](https://mise.jdx.dev/) for tooling:

```bash
mise install      # Install dependencies
mise build        # Build the application
mise test         # Run tests
mise check        # Run all checks (vet, staticcheck, gosec, gofumpt, vulncheck)
```
