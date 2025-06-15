# Atlas Entry: Fixing Local Development Environment

## Issue Summary

The local development environment was non-functional. The `pdx` CLI could not connect to the local server, and the server itself was crashing due to a series of configuration and code-level issues. The primary symptoms were:

1.  "Connection refused" errors from the `pdx` CLI.
2.  Server crashes due to missing `DATABASE_URL`.
3.  Server crashes due to database password authentication failure.
4.  Server crashes due to a "not-null constraint" violation in the `email_verifications` table.
5.  Persistent errors due to a stale build cache, causing old code to be executed.

## Fix Summary

A multi-step process was required to resolve the issues:

1.  **Environment Configuration:**
    *   The `.env` file was correctly configured with the `DATABASE_URL`.
    *   The PostgreSQL database password was reset and updated in the `.env` file.

2.  **Code Fixes:**
    *   The "not-null constraint" bug was fixed by modifying the `INSERT` statements in `app/server/db/auth_helpers.go` to correctly handle the `id` column.
    *   A new `pdx accounts clear` command was added to the CLI to allow clearing of stale, invalid authentication tokens.

3.  **Build and Deployment:**
    *   The root cause of the persistent errors was identified as a stale build cache and the use of a pre-built Docker image.
    *   The `start_local.sh` script was modified to build the `plandex-server` image from local source instead of pulling it from a registry.
    *   The local environment was completely reset using `docker compose down -v` to ensure a clean slate.
    *   The server was rebuilt and restarted using the corrected scripts.