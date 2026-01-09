#!/bin/bash
# Interactive test script to verify log-tailer functionality
# This script allows you to manually add log entries and see if they're picked up

set -e

TESTFILE="/tmp/test_postgres_interactive.log"
PROJECT_ID="test-project"

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Log Tailer Interactive Test ===${NC}"
echo ""

# Clean up any existing test file
if [ -f "$TESTFILE" ]; then
    echo -e "${YELLOW}Removing existing test file...${NC}"
    rm "$TESTFILE"
fi

# Create an empty log file
touch "$TESTFILE"
echo -e "${GREEN}Created empty test file: $TESTFILE${NC}"
echo ""

# Build the log-tailer if not already built
if [ ! -f "./log-tailer" ]; then
    echo -e "${YELLOW}Building log-tailer...${NC}"
    go build -o log-tailer main.go
    echo -e "${GREEN}Build complete${NC}"
    echo ""
fi

# Start the log tailer in the background
echo -e "${BLUE}Starting log-tailer in background...${NC}"
./log-tailer --log-file="$TESTFILE" --project-id="$PROJECT_ID" > /tmp/tailer_output.log 2>&1 &
TAILER_PID=$!

# Function to cleanup on exit
cleanup() {
    echo ""
    echo -e "${YELLOW}Cleaning up...${NC}"
    kill $TAILER_PID 2>/dev/null || true
    echo -e "${GREEN}Log tailer stopped (PID: $TAILER_PID)${NC}"
    echo ""
    echo -e "${BLUE}Log tailer output:${NC}"
    cat /tmp/tailer_output.log
    echo ""
}

trap cleanup EXIT

# Wait a moment for tailer to start
sleep 2

# Check if tailer is still running
if ! kill -0 $TAILER_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Log tailer failed to start!${NC}"
    cat /tmp/tailer_output.log
    exit 1
fi

echo -e "${GREEN}Log tailer started (PID: $TAILER_PID)${NC}"
echo -e "${BLUE}Output is being written to: /tmp/tailer_output.log${NC}"
echo ""

# Function to add a log entry
add_log_entry() {
    local entry_num=$1
    local entry_type=$2
    local timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)

    if [ "$entry_type" = "audit" ]; then
        local message="AUDIT: SESSION,$entry_num,1,READ,SELECT,TABLE,public.users,SELECT * FROM users WHERE id = $entry_num"
        echo "{\"timestamp\": \"$timestamp\", \"user\": \"testuser\", \"dbname\": \"testdb\", \"message\": \"$message\", \"backend_type\": \"client backend\"}" >> "$TESTFILE"
        echo -e "${GREEN}Added AUDIT log entry #$entry_num${NC}"
    else
        local message="Regular log entry number $entry_num"
        echo "{\"timestamp\": \"$timestamp\", \"user\": \"testuser\", \"dbname\": \"testdb\", \"message\": \"$message\", \"backend_type\": \"client backend\"}" >> "$TESTFILE"
        echo -e "${GREEN}Added regular log entry #$entry_num${NC}"
    fi

    # Show current file size
    local size=$(wc -c < "$TESTFILE" | tr -d ' ')
    echo -e "${BLUE}File size: $size bytes${NC}"
}

# Interactive mode
echo -e "${YELLOW}Interactive Test Mode${NC}"
echo -e "Commands:"
echo -e "  ${GREEN}1${NC} - Add a regular log entry"
echo -e "  ${GREEN}2${NC} - Add an AUDIT log entry"
echo -e "  ${GREEN}3${NC} - Add 5 regular entries quickly"
echo -e "  ${GREEN}4${NC} - Add 5 AUDIT entries quickly"
echo -e "  ${GREEN}5${NC} - Show tailer output"
echo -e "  ${GREEN}6${NC} - Show test file size"
echo -e "  ${GREEN}q${NC} - Quit"
echo ""

entry_counter=1

while true; do
    echo -n -e "${YELLOW}Enter command (1-6 or q): ${NC}"
    read -r command

    case "$command" in
        1)
            add_log_entry $entry_counter "regular"
            entry_counter=$((entry_counter + 1))
            sleep 0.5
            echo ""
            ;;
        2)
            add_log_entry $entry_counter "audit"
            entry_counter=$((entry_counter + 1))
            sleep 0.5
            echo ""
            ;;
        3)
            echo -e "${BLUE}Adding 5 regular entries...${NC}"
            for i in {1..5}; do
                add_log_entry $entry_counter "regular"
                entry_counter=$((entry_counter + 1))
                sleep 0.2
            done
            echo ""
            ;;
        4)
            echo -e "${BLUE}Adding 5 AUDIT entries...${NC}"
            for i in {1..5}; do
                add_log_entry $entry_counter "audit"
                entry_counter=$((entry_counter + 1))
                sleep 0.2
            done
            echo ""
            ;;
        5)
            echo -e "${BLUE}=== Tailer Output (last 30 lines) ===${NC}"
            tail -30 /tmp/tailer_output.log
            echo ""
            ;;
        6)
            local size=$(wc -c < "$TESTFILE" | tr -d ' ')
            local lines=$(wc -l < "$TESTFILE" | tr -d ' ')
            echo -e "${BLUE}Test file: $TESTFILE${NC}"
            echo -e "${BLUE}Size: $size bytes, Lines: $lines${NC}"
            echo ""
            ;;
        q|Q)
            echo -e "${GREEN}Exiting...${NC}"
            break
            ;;
        *)
            echo -e "${RED}Invalid command${NC}"
            echo ""
            ;;
    esac
done

