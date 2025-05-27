# Plandex-Gemini Integration: Debugging Log & Context Guide

## 1. Project Goal

To successfully configure a source-built Plandex CLI and Plandex Server to interact with Google's Gemini API via its OpenAI-compatible endpoint.

## 2. Problem Summary

The Plandex server, when attempting to communicate with the Gemini API at `https://generativelanguage.googleapis.com/v1beta/openai/`, receives a `400 Bad Request` HTTP error. The specific error message from Google is: `"* GenerateContentRequest.contents: contents is not specified\n"`. This occurs even for simple prompts.

## 3. Timeline & Approaches Tried

*   **Initial Setup:**
    *   Source-built Plandex Server (from [`/home/new/plandex-build-experiment/app/server/`](/home/new/plandex-build-experiment/app/server/)) and Plandex CLI (from [`/home/new/plandex-build-experiment/app/cli/`](/home/new/plandex-build-experiment/app/cli/)) were compiled.
    *   The server was configured to run on `http://localhost:8099`.
*   **Gemini Model Configuration (CLI):**
    *   A custom model was added to Plandex (e.g., `google-gemini-debug/gemini-2.5-pro-preview-05-06`) with:
        *   Provider Type: `custom`
        *   Base URL: `https://generativelanguage.googleapis.com/v1beta/openai/`
        *   API Key Env Var: `GEMINI_API_KEY`
        *   Preferred Output Format: Tested with `XML` and `Tool Call JSON`.
*   **Testing & Error Observation:**
    *   Simple prompts (e.g., `pdx t hi`) consistently resulted in the `400 Bad Request`.
*   **Debugging & Diagnosis:**
    *   Custom logging was added to the Plandex server's [`model/client.go`](/home/new/plandex-build-experiment/app/server/model/client.go:1) (commit `5f161d2`) to inspect the outgoing request URL, headers, and body.
    *   Server logs (e.g., [`/home/new/plandex-build-experiment/app/server/source_server.log`](/home/new/plandex-build-experiment/app/server/source_server.log)) revealed the exact JSON payload being sent.
    *   **Payload Analysis:** The `messages[].content` field was structured as an array of objects (e.g., `[{"type":"text", "text":"You are an AI namer..."}]`). This is typical for OpenAI multi-modal content but incorrect for simple text messages to Google's OpenAI-compatible Gemini endpoint, which expects a plain string (e.g., `"You are an AI namer..."`).

## 4. Key Errors & Issues Faced

*   **Primary Error:** HTTP `400 Bad Request` from `https://generativelanguage.googleapis.com/v1beta/openai/`.
    *   Error Message: `"* GenerateContentRequest.contents: contents is not specified\n"`.
*   **Initial Diagnostic Challenge:** Pinpointing the exact cause (e.g., model configuration, API key, headers, or payload structure) required inspecting the raw request. The custom server logging was crucial for this.

## 5. Current State (as of 2024-05-27)

*   **Root Cause Identified:** The Plandex server incorrectly formats the `messages[].content` field in the JSON payload for `custom` providers when the content is simple text. It sends an array of parts instead of a simple string, which Google's API interprets as `contents` not being specified correctly.
*   **Plandex Server Source:** [`/home/new/plandex-build-experiment/app/server/`](/home/new/plandex-build-experiment/app/server/)
*   **Plandex CLI Source:** [`/home/new/plandex-build-experiment/app/cli/`](/home/new/plandex-build-experiment/app/cli/)
*   **Key Files for Modification:**
    *   [`app/server/model/client.go`](/home/new/plandex-build-experiment/app/server/model/client.go:1) (logic for sending requests)
    *   [`app/server/types/message.go`](/home/new/plandex-build-experiment/app/server/types/message.go:1) (defines message structures like `ExtendedChatMessage` and its `ToOpenAI()` method)
*   **Environment Variables:** All necessary environment variables (`GEMINI_API_KEY`, `DATABASE_URL`, `LOCAL_MODE`, `GOENV`, `PLANDEX_HOST`, `PLANDEX_API_HOST`, `PLANDEX_BASE_DIR`) and their typical values are documented in the handoff notes.

## 6. Current Plan & Hypothesis

*   **Hypothesis:** Modifying the Plandex server to correctly format the `messages[].content` as a simple string for text-only messages when using `custom` providers (specifically targeting the Gemini OpenAI-compatible endpoint) will resolve the `400 Bad Request` error.
*   **Plan:**
    1.  **Modify Code:**
        *   Target the `createChatCompletionStreamExtended` function in [`app/server/model/client.go`](/home/new/plandex-build-experiment/app/server/model/client.go:1).
        *   In the `else` block for `custom` providers (around line 188 of the original file, or near line 81 of `gemini_debug_handoff.md`'s context), instead of directly marshalling `extendedReq`, construct a temporary anonymous struct for marshalling.
        *   This temporary struct will mirror the fields expected by the OpenAI API (e.g., `Model`, `Messages`, `Temperature`, etc.).
        *   The `Messages` field of this temporary struct will be of type `[]openai.ChatCompletionMessage`.
        *   Iterate through `extendedReq.Messages`. For each `msg` of type `types.ExtendedChatMessage`, call `*msg.ToOpenAI()` (this method is already defined in [`app/server/types/message.go`](/home/new/plandex-build-experiment/app/server/types/message.go:1) and correctly simplifies single text part content to a string). Assign the result to the corresponding message in the temporary struct.
        *   Ensure all other necessary fields from `extendedReq` are copied to this temporary struct.
        *   Marshal this temporary struct to `jsonBody`.
    2.  **Build Server:** Recompile the Plandex server.
    3.  **Test:**
        *   Run the modified server with the correct environment.
        *   Use the Plandex CLI to send a test prompt (e.g., `pdx t hi`).
        *   Check server logs for the updated request payload structure.
        *   Verify if the "Contents is not specified" error is resolved and Gemini responds successfully.
    4.  **Complete Original Task:** If successful, proceed with any remaining original task items, such as creating specific test plans or verifying model settings.

## 7. Applied Diffs / Code Changes

*   **Change 1 (2024-05-27):**
    *   **File:** [`app/server/model/client.go`](/home/new/plandex-build-experiment/app/server/model/client.go:1)
    *   **Function:** `createChatCompletionStreamExtended`
    *   **Lines Modified:** The `else` block starting around line 188 (original file version) which handles `custom` providers.
    *   **Summary:**
        *   Modified the JSON marshalling logic for `custom` providers.
        *   Instead of directly marshalling the `extendedReq` (which uses `types.ExtendedChatMessage` leading to `content` being an array of parts), a temporary anonymous struct (`tempReqForMarshal`) is now created.
        *   This `tempReqForMarshal` is populated with fields from `extendedReq`, but its `Messages` field is constructed by iterating over `extendedReq.Messages` and calling `extMsg.ToOpenAI()` for each message.
        *   The `ToOpenAI()` method converts the Plandex message to the standard `openai.ChatCompletionMessage` format, ensuring that simple text content is represented as a plain string (e.g., `"content": "text..."`) rather than an array of objects.
        *   This temporary struct, with correctly formatted messages, is then marshaled to JSON.
        *   **Purpose:** To ensure the payload sent to OpenAI-compatible custom providers (like Google Gemini) has the `messages[].content` field as a simple string for text messages, resolving the "contents not specified" error.

## 8. Context Guide for Future Reference

*   **System Overview:**
    *   **Plandex CLI:** User interface for interacting with the Plandex system.
    *   **Plandex Server:** Backend service that manages plans, interacts with LLMs, etc.
    *   **Gemini API:** Google's Large Language Model, accessed via its OpenAI-compatible endpoint.
*   **Key File & Executable Locations:**
    *   Server Source Code: [`/home/new/plandex-build-experiment/app/server/`](/home/new/plandex-build-experiment/app/server/)
    *   CLI Source Code: [`/home/new/plandex-build-experiment/app/cli/`](/home/new/plandex-build-experiment/app/cli/)
    *   Server Executable (source build): [`/home/new/plandex-build-experiment/app/server/plandex_server_with_logging`](/home/new/plandex-build-experiment/app/server/plandex_server_with_logging)
    *   CLI Executable (source build): [`/home/new/plandex-build-experiment/app/cli/plandex_cli_with_logging`](/home/new/plandex-build-experiment/app/cli/plandex_cli_with_logging)
    *   Server Logs: Typically in [`/home/new/plandex-build-experiment/app/server/source_server.log`](/home/new/plandex-build-experiment/app/server/source_server.log) or a timestamped subdirectory like [`app/server/server_logs/run_YYYYMMDD_HHMMSS/plandex_server.log`](app/server/server_logs/run_YYYYMMDD_HHMMSS/plandex_server.log).
*   **Critical Environment Variables:**
    *   For Server:
        *   `GEMINI_API_KEY`: Your Google Gemini API key.
        *   `DATABASE_URL="postgresql://plandex:plandex@localhost:5433/plandex?sslmode=disable"` (or similar, for Dockerized PostgreSQL).
        *   `LOCAL_MODE="1"`: Enables local authentication.
        *   `GOENV="development"`: Recommended for development builds.
        *   `PLANDEX_BASE_DIR="/tmp/plandex_server_data"` (or other chosen path for server data).
    *   For CLI:
        *   `PLANDEX_HOST="http://localhost:8099"` (points to the local server).
        *   `PLANDEX_API_HOST="http://localhost:8099"` (points to the local server API).
*   **Build Process (Server):**
    ```bash
    cd /home/new/plandex-build-experiment/app/server
    go build -o ./plandex_server_with_logging .
    ```
*   **General Testing Workflow:**
    1.  Ensure PostgreSQL is running (e.g., Docker container `app_plandex-postgres_1` on host port `5433`).
    2.  Start the Plandex Server with the correct environment variables.
    3.  In a separate terminal, configure and use the Plandex CLI with its environment variables.
    4.  Typical CLI sequence for testing a new model:
        ```bash
        # ./plandex_cli_with_logging login local # If not already logged in or session expired
        # ./plandex_cli_with_logging models add custom <provider_name>/<model_name> --base-url <gemini_endpoint> --api-key-env-var GEMINI_API_KEY --output-format 'Tool Call JSON'
        # ./plandex_cli_with_logging packs add <pack_name> --model <provider_name>/<model_name>
        # ./plandex_cli_with_logging plans add <plan_name> --pack <pack_name>
        ./plandex_cli_with_logging t "your test prompt" --plan <plan_name>
        ```
    5.  Monitor server logs for request/response details and the CLI for output.
*   **Relevant Code Logic Points:**
    *   **Current Marshalling (Problematic for Custom Gemini):** In [`app/server/model/client.go`](/home/new/plandex-build-experiment/app/server/model/client.go:1), within `createChatCompletionStreamExtended`, the `else` block for `custom` providers directly marshals `extendedReq`.
    *   **Key Conversion Method:** `types.ExtendedChatMessage.ToOpenAI()` found in [`app/server/types/message.go`](/home/new/plandex-build-experiment/app/server/types/message.go:1). This method correctly converts a Plandex message to the `openai.ChatCompletionMessage` format, including simplifying `Content` to a string if it's single-part text.
    *   **Proposed Fix Structure:** The conceptual Go snippet provided in `specs/gemini_debug_handoff.md` (lines 92-151) outlines creating a temporary struct populated with data from `extendedReq`, using `ToOpenAI()` for messages, and then marshalling this temporary struct.