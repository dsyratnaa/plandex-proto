#!/bin/bash

# Plandex Server Startup Script with Logfire Tracing
# This script starts the Plandex server with OpenTelemetry tracing enabled

echo "🚀 Starting Plandex Server with Logfire Tracing..."

# Set the Logfire token (write token for sending traces)
export LOGFIRE_TOKEN=pylf_v1_us_1Th1dn0glpzT1PZTJ4FFXZldbvWqSr9WVfk51qDLHy9V

# Optional: Set service name (defaults to "plandex-server")
export OTEL_SERVICE_NAME=plandex-server

# Optional: Set custom OTLP endpoint (defaults to Logfire)
# export OTEL_EXPORTER_OTLP_ENDPOINT=https://logfire-api.pydantic.dev

# Database configuration (using Docker PostgreSQL on port 5433)
export DATABASE_URL="postgres://plandex:plandex@localhost:5433/plandex?sslmode=disable"
export GOENV=development
export LOCAL_MODE=1
export PLANDEX_BASE_DIR=/plandex-server

echo "📊 Configuration:"
echo "  - Service Name: ${OTEL_SERVICE_NAME:-plandex-server}"
echo "  - Logfire Token: ${LOGFIRE_TOKEN:0:20}..."
echo "  - OTLP Endpoint: ${OTEL_EXPORTER_OTLP_ENDPOINT:-https://logfire-api.pydantic.dev (default)}"
echo "  - Database URL: ${DATABASE_URL}"
echo "  - Local Mode: ${LOCAL_MODE}"
echo ""

# Build and start the server
echo "🔨 Building server..."
go build -o plandex-server-traced .

if [ $? -eq 0 ]; then
    echo "✅ Build successful!"
    echo "🌟 Starting server with tracing enabled..."
    echo "📈 View traces at: https://logfire.pydantic.dev/"
    echo ""
    ./plandex-server-traced
else
    echo "❌ Build failed!"
    exit 1
fi
