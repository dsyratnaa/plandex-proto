
**Strategic Logfire Usage for Debugging (via Hypothetical CLI/API)**

This section outlines how you (or an AI agent like Plandex, if it could interact with a Logfire API/CLI) would use programmatic queries to debug the Plandex server's functionality and tracing implementation.

**Assumptions:**

*   Logfire provides a CLI tool (e.g., `logfire-cli`) or a queryable API.
*   The query syntax supports filtering by time, service name, span name, attributes, trace ID, and error status.
*   Output can be (at least partially) structured (e.g., JSON) for easier parsing by an agent.

**Core Debugging Flows using Logfire Programmatically:**

**Flow 1: Verifying Basic Trace Ingestion & HTTP Request Spans**

*   **Goal:** Ensure traces are reaching Logfire and basic HTTP ingress is captured.
*   **Trigger:** Start the Plandex server. Make a `curl http://localhost:8080/health` (or any simple endpoint).
*   **Logfire CLI/API Actions (Hypothetical Commands):**
    1.  **Check for recent traces from the service:**
        ```bash
        # Hypothetical CLI command
        logfire-cli traces list --service-name "plandex-server" --since 5m --limit 1
        # API Equivalent: GET /traces?service_name=plandex-server&since=5m&limit=1
        ```
        *   **Plandex Debug Logic:** If no traces are returned, there's an issue with OTel initialization, exporter config, or network. Log this as a critical setup failure.
    2.  **Verify the specific `/health` request trace:**
        ```bash
        logfire-cli traces list --service-name "plandex-server" \
            --attribute http.target="/health" \
            --attribute http.method="GET" \
            --attribute http.status_code=200 \
            --span-name "http.server.request" \
            --since 1m --limit 1 --output json
        ```
        *   **Plandex Debug Logic:**
            *   If no trace found: "Test failed: `/health` request did not produce the expected trace."
            *   If trace found, parse JSON output: "Test passed: `/health` trace found with correct attributes."
    3.  **Verify a 404 request trace:**
        *   Trigger: `curl http://localhost:8080/nonexistentpath`
        ```bash
        logfire-cli traces list --service-name "plandex-server" \
            --attribute http.target="/nonexistentpath" \
            --attribute http.status_code=404 \
            --span-name "http.server.request" \
            --since 1m --limit 1 --output json
        ```
        *   **Plandex Debug Logic:** Similar success/failure checks.

**Flow 2: Verifying Service Logic Spans & Context Propagation**

*   **Goal:** Ensure key business logic functions create correctly parented child spans with expected attributes.
*   **Trigger:** Send a request that invokes `plan.Tell` (e.g., via Plandex CLI or `curl` to the `/api/v1/tell` endpoint).
*   **Logfire CLI/API Actions:**
    1.  **Get the Trace ID of the HTTP request:**
        ```bash
        # Assume the HTTP request span was created and we can get its trace_id
        # This might involve first querying for the HTTP span as in Flow 1, then extracting its trace_id
        TRACE_ID=$(logfire-cli traces list --service-name "plandex-server" \
            --attribute http.target="/api/v1/tell" --since 1m --limit 1 --output json | jq -r .[0].trace_id)
        # (jq is a common CLI JSON processor; the actual extraction depends on output format)
        ```
        *   **Plandex Debug Logic:** If `TRACE_ID` is empty, the root HTTP span wasn't found.
    2.  **Query for the `plan.Tell` span within that trace:**
        ```bash
        logfire-cli spans list --trace-id "$TRACE_ID" --span-name "plan.Tell" --output json
        ```
        *   **Plandex Debug Logic:**
            *   If no span found: "Test failed: `plan.Tell` span not found for trace $TRACE_ID."
            *   If span found, parse JSON:
                *   Verify `parent_span_id` matches the `span_id` of the `http.server.request` span (would require fetching that span too or assuming its structure).
                *   Verify attributes like `plan.id`, `user.id` are present and have expected values (if known for the test).
    3.  **Recursively check for child spans (e.g., `plan.activatePlan`):**
        ```bash
        logfire-cli spans list --trace-id "$TRACE_ID" --span-name "plan.activatePlan" --output json
        ```
        *   **Plandex Debug Logic:** Verify it's parented by the `plan.Tell` span.

**Flow 3: Debugging LLM Call Issues (e.g., Gemini 400 Error)**

*   **Goal:** Identify failing LLM calls, retrieve their trace context, and correlate with detailed logs.
*   **Trigger:** Send a request known to cause a Gemini 400 error (or any LLM error).
*   **Logfire CLI/API Actions:**
    1.  **Find failing LLM request spans for Gemini:**
        ```bash
        logfire-cli spans list --service-name "plandex-server" \
            --span-name "llm.request" \
            --attribute gen_ai.system="google_gemini" \
            --attribute http.status_code=400 \
            --attribute otel.status_code="ERROR" \
            --since 5m --output json
        ```
        *   **Plandex Debug Logic:**
            *   If no spans found: "No Gemini 400 errors detected in the last 5 minutes."
            *   If spans found, iterate through them:
                *   Extract `trace_id` and `span_id`.
                *   Extract any recorded error message (`error.message` or similar attribute).
                *   Log: "Detected Gemini 400 error. TraceID: [trace_id], SpanID: [span_id], Error: [error_message]. Please check server file logs for this TraceID to see the request body."
                *   **(Advanced Agent):** The agent could then be prompted to *ask the human* to retrieve the specific log lines from the file log using the `trace_id`.
    2.  **Verify a successful LLM call:**
        ```bash
        logfire-cli spans list --service-name "plandex-server" \
            --span-name "llm.request" \
            --attribute gen_ai.system="openai" \
            --attribute http.status_code=200 \
            --attribute otel.status_code="OK" \
            --since 5m --limit 1 --output json
        ```
        *   **Plandex Debug Logic:** Verify attributes like `gen_ai.request.model` are correct.

**Flow 4: Checking for Any Errors in Traces**

*   **Goal:** Get a quick overview of any operations that failed.
*   **Trigger:** After any set of test operations.
*   **Logfire CLI/API Actions:**
    ```bash
    logfire-cli spans list --service-name "plandex-server" \
        --attribute otel.status_code="ERROR" \
        --since 10m --output json
    ```
    *   **Plandex Debug Logic:**
        *   If spans are returned, list their names, trace IDs, and any error messages.
        *   This helps identify unexpected failures in instrumented parts of the code.

**Flow 5: Verifying Database Call Instrumentation (If Phase 4 Implemented)**

*   **Goal:** Ensure DB calls are traced.
*   **Trigger:** Perform an action that reads/writes to the database.
*   **Logfire CLI/API Actions:**
    1.  Get the `trace_id` of the originating HTTP request.
    2.  Query for DB spans within that trace:
        ```bash
        logfire-cli spans list --trace-id "$TRACE_ID" \
            --attribute db.system="sqlite" \
            --output json
        # (Or filter by span names like "db.GetPlan")
        ```
        *   **Plandex Debug Logic:**
            *   Verify spans exist.
            *   Check attributes like `db.operation`, `db.statement` (if logged).
            *   Check for `otel.status_code="ERROR"` on DB spans if a query was expected to fail.

**Using this for Plandex Debugging Its Own Functionality:**

If Plandex (as an AI agent) were to use these flows to debug *itself* or its interactions:

1.  **Plandex (Self-Correction Goal):** "I need to verify that when I generate a plan involving Gemini, the call to Gemini is successful (HTTP 200)."
2.  **Plandex (Internal Action):**
    *   Constructs a test prompt.
    *   Sends the "tell" request to its own server (internally or via loopback).
    *   Waits a moment.
    *   Executes a Logfire query (hypothetically): `logfire-cli spans list --service-name "plandex-server" --span-name "llm.request" --attribute gen_ai.system="google_gemini" --attribute http.target_for_my_test_prompt --since 1m --output json`
3.  **Plandex (Analyzes Result):**
    *   "Query returned a span with `http.status_code=200`. Test passed."
    *   "Query returned a span with `http.status_code=400`. Test failed. Error attribute says 'content not specified'. I need to re-examine the `ToGeminiChatRequest` transformation for this type of prompt or the data I'm passing to it."
    *   "Query returned no `llm.request` span for Gemini. Test failed. Did the request even reach the LLM client code? Or was the provider/model incorrect?"

This programmatic approach allows for more systematic and potentially automated checks of the tracing instrumentation and the server's behavior as reflected in those traces. The key is to adapt these general flow ideas to the specific capabilities and syntax of the Logfire CLI/API.