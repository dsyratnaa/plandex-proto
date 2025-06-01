#!/bin/bash

# Test script for OpenTelemetry tracing setup
echo "Testing Plandex OpenTelemetry Integration"
echo "========================================"

# Check if .env file exists
if [ ! -f ".env" ]; then
    echo "❌ .env file not found. Please create it with the required environment variables."
    exit 1
fi

echo "✅ .env file found"

# Check if binary exists
if [ ! -f "./plandex-server" ]; then
    echo "Building server..."
    go build .
    if [ $? -ne 0 ]; then
        echo "❌ Build failed"
        exit 1
    fi
fi

echo "✅ Server binary ready"

echo ""
echo "To test the tracing setup:"
echo "1. Set your LOGFIRE_TOKEN in .env file"
echo "2. Run: ./plandex-server"
echo "3. In another terminal, test the health endpoint:"
echo "   curl http://localhost:8080/health"
echo "4. Check your Logfire dashboard for traces"
echo ""
echo "Expected behavior:"
echo "- Server starts with 'Tracer initialized successfully' message"
echo "- Health endpoint returns JSON with TraceID"
echo "- Traces appear in Logfire dashboard"