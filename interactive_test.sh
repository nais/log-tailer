#!/bin/bash
# Interactive test script to verify log-tailer functionality
# This script allows you to manually add log entries and see if they're picked up

set -e

# Number of log files to use for round robin
NUM_FILES=3
LOG_FILE_PREFIX="/tmp/test_postgres_interactive_"
LOG_FILE_SUFFIX=".log"
LOG_GLOB="/tmp/test_postgres_interactive_*.log"
PROJECT_ID="test-project"

# Build array of log files
LOG_FILES=()
for i in $(seq 1 $NUM_FILES); do
    LOG_FILES+=("${LOG_FILE_PREFIX}${i}${LOG_FILE_SUFFIX}")
done

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Log Tailer Interactive Test (Round Robin, $NUM_FILES files) ===${NC}"
echo ""

# Clean up any existing test files
for file in "${LOG_FILES[@]}"; do
    if [ -f "$file" ]; then
        echo -e "${YELLOW}Removing existing test file $file...${NC}"
        rm "$file"
    fi
    # Create an empty log file
    touch "$file"
done
echo -e "${GREEN}Created empty test files: ${LOG_FILES[*]}${NC}"
echo ""

# Build the log-tailer if not already built
if [ ! -f "bin/log-tailer" ]; then
    echo -e "${YELLOW}Building log-tailer...${NC}"
    mise run build
    echo -e "${GREEN}Build complete${NC}"
    echo ""
fi

# Start the log tailer in the background with the glob pattern
LOG_TAILER_OUT="/tmp/tailer_output.log"
echo -e "${BLUE}Starting log-tailer in background (glob: $LOG_GLOB)...${NC}"
bin/log-tailer "$LOG_GLOB" --project-id="$PROJECT_ID" > "$LOG_TAILER_OUT" 2>&1 &
TAILER_PID=$!

# Function to cleanup on exit
cleanup() {
    echo ""
    echo -e "${YELLOW}Cleaning up...${NC}"
    kill $TAILER_PID 2>/dev/null || true
    echo -e "${GREEN}Log tailer stopped (PID: $TAILER_PID)${NC}"
    echo ""
    echo -e "${BLUE}Log tailer output:${NC}"
    cat "$LOG_TAILER_OUT"
    echo ""
    for file in "${LOG_FILES[@]}"; do
        rm -f "$file"
    done
}

trap cleanup EXIT

# Wait a moment for tailer to start
sleep 2

# Check if tailer is still running
if ! kill -0 $TAILER_PID 2>/dev/null; then
    echo -e "${RED}ERROR: Log tailer failed to start!${NC}"
    cat "$LOG_TAILER_OUT"
    exit 1
fi

echo -e "${GREEN}Log tailer started (PID: $TAILER_PID)${NC}"
echo -e "${BLUE}Output is being written to: $LOG_TAILER_OUT${NC}"
echo ""

echo -e "${YELLOW}Test files:${NC} ${LOG_FILES[*]}"
echo ""

# Function to add a log entry in round robin fashion
add_log_entry() {
    local entry_num=$1
    local entry_type=$2
    local file_index=$(( (entry_num - 1) % NUM_FILES ))
    local target_file="${LOG_FILES[$file_index]}"
    local timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)

    if [ "$entry_type" = "audit" ]; then
        local message="AUDIT: SESSION,$entry_num,1,READ,SELECT,TABLE,public.users,SELECT * FROM users WHERE id = $entry_num"
        echo "{\"timestamp\": \"$timestamp\", \"user\": \"testuser\", \"dbname\": \"testdb\", \"message\": \"$message\", \"backend_type\": \"client backend\"}" >> "$target_file"
        echo -e "${GREEN}Added AUDIT log entry #$entry_num to $target_file${NC}"
    else
        local message="Regular log entry number $entry_num"
        echo "{\"timestamp\": \"$timestamp\", \"user\": \"testuser\", \"dbname\": \"testdb\", \"message\": \"$message\", \"backend_type\": \"client backend\"}" >> "$target_file"
        echo -e "${GREEN}Added regular log entry #$entry_num to $target_file${NC}"
    fi

    # Show current file size
    local size=$(wc -c < "$target_file" | tr -d ' ')
    echo -e "${BLUE}File size for $target_file: $size bytes${NC}"
}

# Interactive mode
echo -e "${YELLOW}Interactive Test Mode${NC}"
echo -e "Commands:"
echo -e "  ${GREEN}1${NC} - Add a regular log entry"
echo -e "  ${GREEN}2${NC} - Add an AUDIT log entry"
echo -e "  ${GREEN}3${NC} - Add 5 regular entries quickly"
echo -e "  ${GREEN}4${NC} - Add 5 AUDIT entries quickly"
echo -e "  ${GREEN}5${NC} - Show tailer output"
echo -e "  ${GREEN}6${NC} - Show test file sizes"
echo -e "  ${GREEN}7${NC} - Add a new log file to the distribution"
echo -e "  ${GREEN}q${NC} - Quit"
echo ""

entry_counter=1

add_new_log_file() {
    local new_index=$((NUM_FILES + 1))
    local new_file="${LOG_FILE_PREFIX}${new_index}${LOG_FILE_SUFFIX}"
    touch "$new_file"
    LOG_FILES+=("$new_file")
    NUM_FILES=$new_index
    echo -e "${GREEN}Added new log file: $new_file${NC}"
}

while true; do
    echo -n -e "${YELLOW}Enter command (1-7 or q): ${NC}"
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
            tail -30 "$LOG_TAILER_OUT"
            echo ""
            ;;
        6)
            for file in "${LOG_FILES[@]}"; do
                size=$(wc -c < "$file" | tr -d ' ')
                lines=$(wc -l < "$file" | tr -d ' ')
                echo -e "${BLUE}Test file: $file${NC}"
                echo -e "${BLUE}Size: $size bytes, Lines: $lines${NC}"
            done
            echo ""
            ;;
        7)
            add_new_log_file
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

