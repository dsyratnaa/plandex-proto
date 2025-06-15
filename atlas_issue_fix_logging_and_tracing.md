# Atlas Entry: Restoring Server Logging and Tracing

## Issue Summary

After a series of debugging steps that involved switching between running the server from a local source build and running it from a Docker container, the server logging and Logfire tracing stopped working.

*   **Server Logging:** Logs were no longer being saved to the `app/server/server_logs` directory.
*   **Logfire Tracing:** Traces were no longer appearing in the Pydantic dashboard.

## Root Cause Analysis

The root cause of both issues was a change in how the server was being executed.

*   **Server Logging:** When running the server binary directly from the command line (e.g., `./plandex-server-fixed`), the standard output and standard error streams were not being redirected to a log file.
*   **Logfire Tracing:** When running the server via `docker compose`, the necessary Logfire/Pydantic environment variables (`LOGFIRE_TOKEN`, `OTEL_SERVICE_NAME`, etc.) were not being passed from the host machine into the Docker container.

## Fix Summary

The solution was to return to the established source build workflow and ensure that the server was started with the correct environment and output redirection.

1.  **Created a Runner Script:** A new script, `app/server/run_server_with_logging.sh`, was created to encapsulate the server startup logic.
2.  **Redirected Output:** This script redirects both `stdout` and `stderr` from the server process to a timestamped log file within the `app/server/server_logs` directory.
3.  **Loaded Environment Variables:** The script uses `export $(grep -v '^#' .env | xargs)` to ensure that all necessary environment variables, including those for Logfire tracing, are loaded from the `.env` file before the server is started.
4.  **Made Script Executable:** The script was made executable using `chmod +x`.

This approach restored both the file-based logging and the Logfire tracing, providing the necessary visibility for further debugging.