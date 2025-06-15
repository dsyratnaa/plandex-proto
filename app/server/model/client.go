package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"plandex-server/types"
	"strings"
	"sync"
	"time"

	shared "plandex-shared"

	"github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// note that we are *only* using streaming requests now
// non-streaming request handling has been removed completely
// streams offer more predictable cancellation partial results

const (
	ACTIVE_STREAM_CHUNK_TIMEOUT          = time.Duration(60) * time.Second
	USAGE_CHUNK_TIMEOUT                  = time.Duration(10) * time.Second
	MAX_ADDITIONAL_RETRIES_WITH_FALLBACK = 1
	MAX_RETRIES_WITHOUT_FALLBACK         = 2
	MAX_RETRY_DELAY_SECONDS              = 10
)

var httpClient = &http.Client{}

type ClientInfo struct {
	Client   *openai.Client
	ApiKey   string
	OrgId    string
	Endpoint string
}

func InitClients(apiKeys map[string]string, endpointsByApiKeyEnvVar map[string]string, openAIEndpoint, orgId string) map[string]ClientInfo {
	clients := make(map[string]ClientInfo)
	for key, apiKey := range apiKeys {
		var clientEndpoint string
		var clientOrgId string
		if key == "OPENAI_API_KEY" {
			clientEndpoint = openAIEndpoint
			clientOrgId = orgId
		} else {
			clientEndpoint = endpointsByApiKeyEnvVar[key]
		}
		clients[key] = newClient(apiKey, clientEndpoint, clientOrgId)
	}
	return clients
}

func newClient(apiKey, endpoint, orgId string) ClientInfo {
	config := openai.DefaultConfig(apiKey)
	if endpoint != "" {
		config.BaseURL = endpoint
	}
	if orgId != "" {
		config.OrgID = orgId
	}

	return ClientInfo{
		Client:   openai.NewClientWithConfig(config),
		ApiKey:   apiKey,
		OrgId:    orgId,
		Endpoint: endpoint,
	}
}

// ExtendedChatCompletionStream can wrap either a native OpenAI stream or our custom implementation
type ExtendedChatCompletionStream struct {
	openaiStream *openai.ChatCompletionStream
	customReader *StreamReader[types.ExtendedChatCompletionStreamResponse]
	ctx          context.Context
}

// StreamReader handles the SSE stream reading
type StreamReader[T any] struct {
	reader             *bufio.Reader
	response           *http.Response
	emptyMessagesLimit int
	errAccumulator     *ErrorAccumulator
	unmarshaler        *JSONUnmarshaler
}

// ErrorAccumulator keeps track of errors during streaming
type ErrorAccumulator struct {
	errors []error
	mu     sync.Mutex
}

// JSONUnmarshaler handles JSON unmarshaling for stream responses
type JSONUnmarshaler struct{}

func CreateChatCompletionStream(
	clients map[string]ClientInfo,
	modelConfig *shared.ModelRoleConfig,
	ctx context.Context,
	req types.ExtendedChatCompletionRequest,
) (*ExtendedChatCompletionStream, error) {
	_, ok := clients[modelConfig.BaseModelConfig.ApiKeyEnvVar]
	if !ok {
		fmt.Printf("client not found for api key env var: %s", modelConfig.BaseModelConfig.ApiKeyEnvVar)
		if modelConfig.MissingKeyFallback != nil {
			fmt.Println("using missing key fallback")
			return CreateChatCompletionStream(clients, modelConfig.MissingKeyFallback, ctx, req)
		}
		return nil, fmt.Errorf("client not found for api key env var: %s", modelConfig.BaseModelConfig.ApiKeyEnvVar)
	}

	resolveReq(&req, modelConfig)

	// choose the fastest provider by latency/throughput on openrouter
	if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenRouter {
		req.Model += ":nitro"
	}

	if modelConfig.BaseModelConfig.IncludeReasoning {
		req.IncludeReasoning = true
	}

	return withStreamingRetries(ctx, func(numTotalRetry int, modelErr *shared.ModelError, stripCacheControl bool) (*ExtendedChatCompletionStream, shared.FallbackResult, error) {
		fallbackRes := modelConfig.GetFallbackForModelError(numTotalRetry, modelErr)
		resolvedModelConfig := fallbackRes.ModelRoleConfig

		if resolvedModelConfig == nil {
			return nil, fallbackRes, fmt.Errorf("model config is nil")
		}

		opClient, ok := clients[resolvedModelConfig.BaseModelConfig.ApiKeyEnvVar]

		if !ok {
			if resolvedModelConfig.MissingKeyFallback != nil {
				fmt.Println("using missing key fallback")
				resolvedModelConfig = resolvedModelConfig.MissingKeyFallback
				opClient, ok = clients[resolvedModelConfig.BaseModelConfig.ApiKeyEnvVar]
				if !ok {
					return nil, fallbackRes, fmt.Errorf("client not found for api key env var: %s", resolvedModelConfig.BaseModelConfig.ApiKeyEnvVar)
				}
			} else {
				return nil, fallbackRes, fmt.Errorf("client not found for api key env var: %s", resolvedModelConfig.BaseModelConfig.ApiKeyEnvVar)
			}
		}

		if stripCacheControl {
			for i := range req.Messages {
				for j := range req.Messages[i].Content {
					if req.Messages[i].Content[j].CacheControl != nil {
						req.Messages[i].Content[j].CacheControl = nil
					}
				}
			}
		}

		modelConfig = resolvedModelConfig
		resp, err := createChatCompletionStreamExtended(resolvedModelConfig, opClient, resolvedModelConfig.BaseModelConfig.BaseUrl, ctx, req)
		return resp, fallbackRes, err
	}, func(resp *ExtendedChatCompletionStream, err error) {})
}

func createChatCompletionStreamExtended(
	modelConfig *shared.ModelRoleConfig,
	client ClientInfo,
	baseUrl string,
	ctx context.Context,
	extendedReq types.ExtendedChatCompletionRequest,
) (*ExtendedChatCompletionStream, error) {
	// Start OpenTelemetry span for LLM API call
	tracer := otel.Tracer("plandex-server")
	ctx, span := tracer.Start(ctx, "llm.chat_completion_stream",
		trace.WithAttributes(
			attribute.String("llm.provider", string(modelConfig.BaseModelConfig.Provider)),
			attribute.String("llm.model", string(extendedReq.Model)),
			attribute.String("llm.api_key_env_var", modelConfig.BaseModelConfig.ApiKeyEnvVar),
			attribute.String("llm.base_url", baseUrl),
			attribute.Int("llm.message_count", len(extendedReq.Messages)),
			attribute.Float64("llm.temperature", float64(extendedReq.Temperature)),
			attribute.Float64("llm.top_p", float64(extendedReq.TopP)),
			attribute.Bool("llm.stream", extendedReq.Stream),
		),
	)
	defer span.End()

	log.Printf("LLM API call starting - Model: %s, Provider: %s (TraceID: %s)",
		extendedReq.Model, modelConfig.BaseModelConfig.Provider, span.SpanContext().TraceID().String())
	var openaiReq *types.ExtendedOpenAIChatCompletionRequest
	if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenAI && !modelConfig.BaseModelConfig.UsesOpenAIResponsesAPI {
		openaiReq = extendedReq.ToOpenAI()
		log.Println("Creating chat completion stream with direct OpenAI provider request")
	}

	// Marshal the request body to JSON
	var jsonBody []byte
	var err error
	if openaiReq != nil {
		jsonBody, err = json.Marshal(openaiReq)
	} else {
		// For custom providers, or any other case not handled by direct OpenAI,
		// we need to ensure messages are in the standard OpenAI format,
		// especially for content (string vs. array of parts).
		// The ToOpenAI() method on ExtendedChatMessage handles this.
		// We construct a temporary struct that mirrors openai.ChatCompletionRequest
		// to ensure correct marshalling.
		tempReqForMarshal := struct {
			Model            string                               `json:"model"`
			Messages         []openai.ChatCompletionMessage       `json:"messages"`
			MaxTokens        int                                  `json:"max_tokens,omitempty"`
			Temperature      float32                              `json:"temperature,omitempty"`
			TopP             float32                              `json:"top_p,omitempty"`
			N                int                                  `json:"n,omitempty"`
			Stream           bool                                 `json:"stream,omitempty"`
			Stop             []string                             `json:"stop,omitempty"`
			PresencePenalty  float32                              `json:"presence_penalty,omitempty"`
			FrequencyPenalty float32                              `json:"frequency_penalty,omitempty"`
			LogitBias        map[string]int                       `json:"logit_bias,omitempty"`
			User             string                               `json:"user,omitempty"`
			Seed             *int                                 `json:"seed,omitempty"`
			Tools            []openai.Tool                        `json:"tools,omitempty"`
			ToolChoice       interface{}                          `json:"tool_choice,omitempty"` // Can be string or object (e.g. openai.ToolChoice)
			ResponseFormat   *openai.ChatCompletionResponseFormat `json:"response_format,omitempty"`
		}{
			Model:            string(extendedReq.Model),
			MaxTokens:        extendedReq.MaxTokens,
			Temperature:      extendedReq.Temperature,
			TopP:             extendedReq.TopP,
			N:                extendedReq.N,
			Stream:           extendedReq.Stream, // This should generally be true for streaming
			Stop:             extendedReq.Stop,
			PresencePenalty:  extendedReq.PresencePenalty,
			FrequencyPenalty: extendedReq.FrequencyPenalty,
			LogitBias:        extendedReq.LogitBias,
			User:             extendedReq.User,
			Seed:             extendedReq.Seed,
			Tools:            extendedReq.Tools,
			ToolChoice:       extendedReq.ToolChoice,
			ResponseFormat:   extendedReq.ResponseFormat,
		}

		tempReqForMarshal.Messages = make([]openai.ChatCompletionMessage, len(extendedReq.Messages))
		for i, extMsg := range extendedReq.Messages {
			// extMsg is of type types.ExtendedChatMessage
			// ToOpenAI() converts it to *openai.ChatCompletionMessage,
			// correctly handling the Content field (string for simple text).
			openAIMsg := extMsg.ToOpenAI()
			if openAIMsg != nil {
				tempReqForMarshal.Messages[i] = *openAIMsg
			}
			// If openAIMsg is nil (e.g., due to an issue with extMsg),
			// it will result in a zero-value ChatCompletionMessage at that index,
			// which might need further error handling depending on API strictness.
		}
		jsonBody, err = json.Marshal(tempReqForMarshal)
	}
	if err != nil {
		return nil, fmt.Errorf("error marshaling request: %w", err)
	}

	// Add request body to tracing attributes
	span.SetAttributes(
		attribute.String("http.request.body.text", string(jsonBody)),
		attribute.Int("http.request.body.size", len(jsonBody)),
	)

	// Log request body for debugging
	log.Printf("LLM Request Body (TraceID: %s): %s", span.SpanContext().TraceID().String(), string(jsonBody))

	// Create new request
	var url string
	if modelConfig.BaseModelConfig.UsesOpenAIResponsesAPI {
		url = baseUrl + "/responses"
	} else {
		url = baseUrl + "/chat/completions"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set required headers for streaming
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Authorization", "Bearer "+client.ApiKey)
	if client.OrgId != "" {
		req.Header.Set("OpenAI-Organization", client.OrgId)
	}

	// Add OpenRouter headers if it's an OpenRouter provider OR
	// if it's a Custom provider named "gemivertest" (or other OpenRouter-compatible custom providers)
	if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenRouter ||
		(modelConfig.BaseModelConfig.Provider == shared.ModelProviderCustom &&
			modelConfig.BaseModelConfig.CustomProvider != nil &&
			(*modelConfig.BaseModelConfig.CustomProvider == "openrouter" || *modelConfig.BaseModelConfig.CustomProvider == "openrouter/auto" || *modelConfig.BaseModelConfig.CustomProvider == "gemivertest")) {
		log.Println("DEBUG: Adding OpenRouter headers for provider:", modelConfig.BaseModelConfig.Provider, "CustomProvider:", modelConfig.BaseModelConfig.CustomProvider)
		addOpenRouterHeaders(req)
	}

	// Send the request
	resp, err := httpClient.Do(req) //nolint:bodyclose // body is closed in stream.Close()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP request failed: %v", err))
		return nil, fmt.Errorf("error making request: %w", err)
	}

	// Add HTTP response attributes to span
	span.SetAttributes(
		attribute.Int("http.status_code", resp.StatusCode),
		attribute.String("http.url", url),
		attribute.String("http.method", "POST"),
	)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, fmt.Sprintf("Failed to read error response: %v", err))
			return nil, fmt.Errorf("error reading error response: %w", err)
		}

		// Add error response body to tracing attributes
		span.SetAttributes(
			attribute.String("http.response.body.text", string(body)),
			attribute.Int("http.response.body.size", len(body)),
		)

		// Log error response for debugging
		log.Printf("LLM Error Response (TraceID: %s): Status %d, Body: %s",
			span.SpanContext().TraceID().String(), resp.StatusCode, string(body))

		httpErr := &HTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
			Header:     resp.Header.Clone(), // retain Retry-After etc.
		}
		span.RecordError(httpErr)
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)))
		return nil, httpErr
	}

	// Mark span as successful
	span.SetStatus(codes.Ok, "LLM API call successful")

	// Log response headers
	// log.Println("Response headers:")
	// for key, values := range resp.Header {
	// 	log.Printf("%s: %v\n", key, values)
	// }

	reader := &StreamReader[types.ExtendedChatCompletionStreamResponse]{
		reader:             bufio.NewReader(resp.Body),
		response:           resp,
		emptyMessagesLimit: 30,
		errAccumulator:     NewErrorAccumulator(),
		unmarshaler:        &JSONUnmarshaler{},
	}

	log.Printf("LLM API call successful - Status: %d (TraceID: %s)",
		resp.StatusCode, span.SpanContext().TraceID().String())

	return &ExtendedChatCompletionStream{
		customReader: reader,
		ctx:          ctx,
	}, nil
}

func NewErrorAccumulator() *ErrorAccumulator {
	return &ErrorAccumulator{
		errors: make([]error, 0),
	}
}

func (ea *ErrorAccumulator) Add(err error) {
	ea.mu.Lock()
	defer ea.mu.Unlock()
	ea.errors = append(ea.errors, err)
}

func (ea *ErrorAccumulator) GetErrors() []error {
	ea.mu.Lock()
	defer ea.mu.Unlock()
	return ea.errors
}

func (ju *JSONUnmarshaler) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// Recv reads from the stream
func (stream *StreamReader[T]) Recv() (*T, error) {
	for {
		line, err := stream.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		// Trim any whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Check for data prefix
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Extract the data
		data := strings.TrimPrefix(line, "data: ")

		// Log stream data for debugging (first few chunks and completion)
		if data == "[DONE]" {
			log.Println("LLM Stream: [DONE] - Stream completed")
		} else {
			// Log first few chunks to see the response format
			if len(data) > 0 {
				log.Printf("LLM Stream Data: %s", data)
			}
		}

		// Check for stream completion
		if data == "[DONE]" {
			return nil, io.EOF
		}

		// Parse the response
		var response T
		err = stream.unmarshaler.Unmarshal([]byte(data), &response)
		if err != nil {
			log.Printf("LLM Stream Parse Error: %v, Data: %s", err, data)
			stream.errAccumulator.Add(err)
			continue
		}

		return &response, nil
	}
}

func (stream *StreamReader[T]) Close() error {
	if stream.response != nil {
		return stream.response.Body.Close()
	}
	return nil
}

// Recv returns the next message in the stream
func (stream *ExtendedChatCompletionStream) Recv() (*types.ExtendedChatCompletionStreamResponse, error) {
	select {
	case <-stream.ctx.Done():
		return nil, stream.ctx.Err()
	default:
		if stream.openaiStream != nil {
			bytes, err := stream.openaiStream.RecvRaw()
			if err != nil {
				return nil, err
			}

			var response types.ExtendedChatCompletionStreamResponse
			err = json.Unmarshal(bytes, &response)
			if err != nil {
				return nil, err
			}
			return &response, nil
		}
		return stream.customReader.Recv()
	}
}

// Close the response body
func (stream *ExtendedChatCompletionStream) Close() error {
	if stream.openaiStream != nil {
		return stream.openaiStream.Close()
	}
	return stream.customReader.Close()
}

func resolveReq(req *types.ExtendedChatCompletionRequest, modelConfig *shared.ModelRoleConfig) {
	// if system prompt is disabled, change the role of the system message to user
	if modelConfig.BaseModelConfig.SystemPromptDisabled {
		log.Println("System prompt disabled - changing role of system message to user")
		for i, msg := range req.Messages {
			log.Println("Message role:", msg.Role)
			if msg.Role == openai.ChatMessageRoleSystem {
				log.Println("Changing role of system message to user")
				req.Messages[i].Role = openai.ChatMessageRoleUser
			}
		}

		for _, msg := range req.Messages {
			log.Println("Final message role:", msg.Role)
		}
	}

	if modelConfig.BaseModelConfig.RoleParamsDisabled {
		log.Println("Role params disabled - setting temperature and top p to 1")
		req.Temperature = 1
		req.TopP = 1
	}
}

func addOpenRouterHeaders(req *http.Request) {
	req.Header.Set("HTTP-Referer", "https://plandex.ai")
	req.Header.Set("X-Title", "Plandex")
	req.Header.Set("X-OR-Prefer", "ttft,throughput")
	if os.Getenv("GOENV") == "production" {
		req.Header.Set("X-OR-Region", "us-east-1")
	}
}
