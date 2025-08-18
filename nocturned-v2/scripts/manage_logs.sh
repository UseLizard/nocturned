#!/bin/bash

# Nocturned-v2 Log Management Tool
# Usage: ./manage_logs.sh [command] [options]

# Device connection details
DEVICE_IP="172.16.42.2"
DEVICE_USER="root"
DEVICE_PASS="nocturne"
LOG_PATH="/var/nocturne/logs/nocturned/current"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
GRAY='\033[0;37m'
NC='\033[0m' # No Color

# Function to colorize log output based on content
colorize_log() {
    while IFS= read -r line; do
        case "$line" in
            *"✅"*|*"Connected to Nocturne"*|*"BLE connection setup complete"*|*"subscriptions active"*)
                echo -e "${GREEN}$line${NC}"
                ;;
            *"❌"*|*"Failed"*|*"Error"*|*"error"*|*"ERROR"*)
                echo -e "${RED}$line${NC}"
                ;;
            *"⚠️"*|*"Warning"*|*"warning"*|*"WARN"*)
                echo -e "${YELLOW}$line${NC}"
                ;;
            *"🔗"*|*"🔧"*|*"Attempting"*|*"Starting"*|*"Scanning"*)
                echo -e "${BLUE}$line${NC}"
                ;;
            *"📨"*|*"📤"*|*"Received"*|*"Sending"*)
                echo -e "${CYAN}$line${NC}"
                ;;
            *"📅"*|*"TimeSync"*|*"TIME_SYNC"*)
                echo -e "${MAGENTA}$line${NC}"
                ;;
            *"FOUND NOCTURNE DEVICE"*)
                echo -e "${WHITE}$line${NC}"
                ;;
            *)
                echo "$line"
                ;;
        esac
    done
}

# Function to show usage
show_usage() {
    echo -e "${GREEN}📋 Nocturned-v2 Log Management Tool${NC}"
    echo
    echo -e "${WHITE}Usage:${NC} $0 <command> [options]"
    echo
    echo -e "${WHITE}Commands:${NC}"
    echo -e "  ${CYAN}read${NC} [lines]     - Read all logs (default: all, or specify number of lines)"
    echo -e "  ${CYAN}tail${NC} [lines]     - Show last N lines (default: 50)"
    echo -e "  ${CYAN}head${NC} [lines]     - Show first N lines (default: 50)"
    echo -e "  ${CYAN}filter${NC} <pattern> - Show logs matching pattern"
    echo -e "  ${CYAN}errors${NC}           - Show only error messages"
    echo -e "  ${CYAN}warnings${NC}         - Show only warnings"
    echo -e "  ${CYAN}success${NC}          - Show only success messages"
    echo -e "  ${CYAN}bluetooth${NC}        - Show only Bluetooth-related messages"
    echo -e "  ${CYAN}timesync${NC}         - Show only TimeSync messages"
    echo -e "  ${CYAN}connections${NC}      - Show only connection-related messages"
    echo -e "  ${CYAN}commands${NC}         - Show only command messages"
    echo -e "  ${CYAN}truncate${NC}         - Clear/truncate the log file (DESTRUCTIVE)"
    echo -e "  ${CYAN}size${NC}             - Show log file size and line count"
    echo -e "  ${CYAN}archive${NC}          - Create a backup copy of current logs"
    echo -e "  ${CYAN}follow${NC}           - Follow logs in real-time (like tail -f)"
    echo
    echo -e "${WHITE}Examples:${NC}"
    echo -e "  $0 read           # Read entire log"
    echo -e "  $0 read 100       # Read last 100 lines"
    echo -e "  $0 tail 30        # Show last 30 lines"
    echo -e "  $0 filter BLE     # Show lines containing 'BLE'"
    echo -e "  $0 errors         # Show only error messages"
    echo -e "  $0 bluetooth      # Show Bluetooth messages"
    echo -e "  $0 truncate       # Clear the log (with confirmation)"
    echo -e "  $0 size           # Show log statistics"
    echo
    echo -e "${YELLOW}Note: truncate command will ask for confirmation before clearing logs${NC}"
}

# Function to execute SSH command
ssh_exec() {
    sshpass -p "$DEVICE_PASS" ssh -o StrictHostKeyChecking=no "$DEVICE_USER@$DEVICE_IP" "$1" 2>/dev/null
}

# Function to test connectivity
test_connectivity() {
    if ! ssh_exec "echo 'test'" &>/dev/null; then
        echo -e "${RED}❌ Cannot connect to Car Thing device at $DEVICE_IP${NC}"
        echo -e "${YELLOW}Please check device connectivity${NC}"
        exit 1
    fi
}

# Function to check if log file exists
check_log_file() {
    if ! ssh_exec "test -f $LOG_PATH"; then
        echo -e "${RED}❌ Log file not found: $LOG_PATH${NC}"
        exit 1
    fi
}

# Function to read logs
read_logs() {
    local lines="$1"
    echo -e "${BLUE}📖 Reading nocturned-v2 logs...${NC}"
    
    if [ -n "$lines" ] && [ "$lines" -gt 0 ]; then
        echo -e "${GRAY}Showing last $lines lines${NC}"
        ssh_exec "tail -n $lines $LOG_PATH" | colorize_log
    else
        echo -e "${GRAY}Showing entire log file${NC}"
        ssh_exec "cat $LOG_PATH" | colorize_log
    fi
}

# Function to show tail
show_tail() {
    local lines="${1:-50}"
    echo -e "${BLUE}📄 Last $lines lines of logs:${NC}"
    ssh_exec "tail -n $lines $LOG_PATH" | colorize_log
}

# Function to show head
show_head() {
    local lines="${1:-50}"
    echo -e "${BLUE}📄 First $lines lines of logs:${NC}"
    ssh_exec "head -n $lines $LOG_PATH" | colorize_log
}

# Function to filter logs
filter_logs() {
    local pattern="$1"
    if [ -z "$pattern" ]; then
        echo -e "${RED}❌ Pattern required for filter command${NC}"
        exit 1
    fi
    
    echo -e "${BLUE}🔍 Filtering logs for pattern: ${WHITE}$pattern${NC}"
    ssh_exec "grep -i '$pattern' $LOG_PATH" | colorize_log
}

# Function to show errors
show_errors() {
    echo -e "${RED}🚨 Error messages:${NC}"
    ssh_exec "grep -i -E '(error|failed|❌)' $LOG_PATH" | colorize_log
}

# Function to show warnings
show_warnings() {
    echo -e "${YELLOW}⚠️ Warning messages:${NC}"
    ssh_exec "grep -i -E '(warning|warn|⚠️)' $LOG_PATH" | colorize_log
}

# Function to show success messages
show_success() {
    echo -e "${GREEN}✅ Success messages:${NC}"
    ssh_exec "grep -E '(✅|Connected|setup complete|successfully)' $LOG_PATH" | colorize_log
}

# Function to show Bluetooth messages
show_bluetooth() {
    echo -e "${BLUE}📡 Bluetooth messages:${NC}"
    ssh_exec "grep -i -E '(bluetooth|ble|🔗|🔧|gatt|characteristic|subscription)' $LOG_PATH" | colorize_log
}

# Function to show TimeSync messages
show_timesync() {
    echo -e "${MAGENTA}📅 TimeSync messages:${NC}"
    ssh_exec "grep -i -E '(timesync|📅|time sync)' $LOG_PATH" | colorize_log
}

# Function to show connection messages
show_connections() {
    echo -e "${CYAN}🔌 Connection messages:${NC}"
    ssh_exec "grep -i -E '(connect|disconnect|websocket|found device|scanning)' $LOG_PATH" | colorize_log
}

# Function to show command messages
show_commands() {
    echo -e "${CYAN}📨 Command messages:${NC}"
    ssh_exec "grep -E '(📨|📤|Received|Sending|command|CMD_)' $LOG_PATH" | colorize_log
}

# Function to truncate logs
truncate_logs() {
    echo -e "${YELLOW}⚠️ WARNING: This will permanently delete all log data!${NC}"
    echo -e "${WHITE}Current log file: $LOG_PATH${NC}"
    
    # Show current log size
    local size_info=$(ssh_exec "ls -lh $LOG_PATH | awk '{print \$5}' && wc -l < $LOG_PATH")
    local file_size=$(echo "$size_info" | head -n1)
    local line_count=$(echo "$size_info" | tail -n1)
    echo -e "${GRAY}Current size: $file_size ($line_count lines)${NC}"
    
    echo
    read -p "Are you sure you want to truncate the log file? (type 'yes' to confirm): " confirm
    
    if [ "$confirm" = "yes" ]; then
        ssh_exec "truncate -s 0 $LOG_PATH"
        if [ $? -eq 0 ]; then
            echo -e "${GREEN}✅ Log file truncated successfully${NC}"
        else
            echo -e "${RED}❌ Failed to truncate log file${NC}"
            exit 1
        fi
    else
        echo -e "${YELLOW}📝 Truncation cancelled${NC}"
    fi
}

# Function to show log size
show_size() {
    echo -e "${BLUE}📊 Log file statistics:${NC}"
    
    local stats=$(ssh_exec "ls -lh $LOG_PATH && echo '---' && wc -l < $LOG_PATH && echo '---' && du -h $LOG_PATH")
    
    if [ -n "$stats" ]; then
        local file_info=$(echo "$stats" | head -n1)
        local line_count=$(echo "$stats" | sed -n '3p')
        local disk_usage=$(echo "$stats" | tail -n1)
        
        echo -e "${WHITE}File:${NC} $LOG_PATH"
        echo -e "${WHITE}Size:${NC} $(echo $file_info | awk '{print $5}')"
        echo -e "${WHITE}Lines:${NC} $line_count"
        echo -e "${WHITE}Disk usage:${NC} $(echo $disk_usage | awk '{print $1}')"
        echo -e "${WHITE}Modified:${NC} $(echo $file_info | awk '{print $6, $7, $8}')"
    else
        echo -e "${RED}❌ Could not retrieve log statistics${NC}"
    fi
}

# Function to archive logs
archive_logs() {
    local timestamp=$(date +"%Y%m%d_%H%M%S")
    local archive_name="nocturned_logs_$timestamp.log"
    local local_path="./logs_archive_$timestamp.log"
    
    echo -e "${BLUE}📦 Creating log archive...${NC}"
    
    # Copy log file locally
    sshpass -p "$DEVICE_PASS" scp -o StrictHostKeyChecking=no "$DEVICE_USER@$DEVICE_IP:$LOG_PATH" "$local_path" 2>/dev/null
    
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}✅ Log archive created: ${WHITE}$local_path${NC}"
        
        # Show archive info
        local size=$(ls -lh "$local_path" | awk '{print $5}')
        local lines=$(wc -l < "$local_path")
        echo -e "${GRAY}Archive size: $size ($lines lines)${NC}"
    else
        echo -e "${RED}❌ Failed to create log archive${NC}"
        exit 1
    fi
}

# Function to follow logs
follow_logs() {
    echo -e "${BLUE}👁️ Following logs in real-time...${NC}"
    echo -e "${YELLOW}Press Ctrl+C to stop${NC}"
    echo -e "${CYAN}===========================================${NC}"
    
    ssh_exec "tail -f $LOG_PATH" | colorize_log
}

# Main script logic

# Check if sshpass is installed
if ! command -v sshpass &> /dev/null; then
    echo -e "${RED}Error: sshpass is not installed${NC}"
    echo "Please install sshpass: sudo apt-get install sshpass"
    exit 1
fi

# Check arguments
if [ $# -eq 0 ]; then
    show_usage
    exit 0
fi

COMMAND="$1"
shift

# Handle help
if [ "$COMMAND" = "-h" ] || [ "$COMMAND" = "--help" ] || [ "$COMMAND" = "help" ]; then
    show_usage
    exit 0
fi

# Test connectivity and log file
test_connectivity
check_log_file

# Execute command
case "$COMMAND" in
    "read")
        read_logs "$1"
        ;;
    "tail")
        show_tail "$1"
        ;;
    "head")
        show_head "$1"
        ;;
    "filter")
        filter_logs "$1"
        ;;
    "errors")
        show_errors
        ;;
    "warnings")
        show_warnings
        ;;
    "success")
        show_success
        ;;
    "bluetooth")
        show_bluetooth
        ;;
    "timesync")
        show_timesync
        ;;
    "connections")
        show_connections
        ;;
    "commands")
        show_commands
        ;;
    "truncate")
        truncate_logs
        ;;
    "size")
        show_size
        ;;
    "archive")
        archive_logs
        ;;
    "follow")
        follow_logs
        ;;
    *)
        echo -e "${RED}❌ Unknown command: $COMMAND${NC}"
        echo
        show_usage
        exit 1
        ;;
esac