#!/bin/bash
set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"

echo "--- Go Environment ---"
go env
echo "----------------------"

echo "--- Cleaning Caches ---"
go clean -cache -modcache -testcache
echo "-----------------------"

echo "--- Building Server (Verbose) ---"
(cd "$SCRIPT_DIR" && go build -v -o plandex-server .)
echo "---------------------------------"

LOG_DIR="$SCRIPT_DIR/server_logs/run_$(date +%Y%m%d_%H%M%S)"
mkdir -p "$LOG_DIR"
LOG_FILE="$LOG_DIR/plandex_server.log"

echo "--- Running Server ---"
set -a
source "$SCRIPT_DIR/.env"
set +a
"$SCRIPT_DIR/plandex-server" &> "$LOG_FILE"