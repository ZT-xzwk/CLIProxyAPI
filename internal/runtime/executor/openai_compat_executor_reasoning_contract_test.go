package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// newReasoningContractExecutor builds a plain openai-compatibility executor.
// The reasoning contract is bound per request through the resolved model info,
// never through executor construction.
func newReasoningContractExecutor(t *testing.T) *OpenAICompatExecutor {
	t.Helper()
	return NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
}

func reasoningContractModelInfo(id, contract string) *registry.ModelInfo {
	return &registry.ModelInfo{
		ID:                id,
		ReasoningContract: contract,
		Thinking:          &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}},
	}
}

// TestOpenAICompatExecutorReasoningContractResponseNormalizesChat covers the
// stored-history + response sides of the reasoning contract over openai-to-openai
// translation: history reasoning is backfilled as reasoning_content upstream and
// the response gains reasoning_content so clients record and pass back that field.
func TestOpenAICompatExecutorReasoningContractResponseNormalizesChat(t *testing.T) {
	for _, tt := range []struct {
		name          string
		contract      string
		upstreamBody  string
		wantField     string
		wantValue     string
		wantUnchanged bool
	}{
		{
			name:         "deepseek contract adds reasoning_content to the response",
			contract:     "deepseek",
			upstreamBody: `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning":"upstream-chain"},"finish_reason":"stop"}]}`,
			wantField:    "reasoning_content", wantValue: "upstream-chain",
		},
		{
			name:         "openrouter contract adds reasoning to the response",
			contract:     "openrouter",
			upstreamBody: `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning_content":"native-chain"},"finish_reason":"stop"}]}`,
			wantField:    "reasoning", wantValue: "native-chain",
		},
		{
			name:          "none contract keeps the response untouched",
			contract:      "",
			upstreamBody:  `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning":"upstream-chain"},"finish_reason":"stop"}]}`,
			wantField:     "reasoning_content",
			wantUnchanged: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.upstreamBody)
			}))
			t.Cleanup(server.Close)

			executor := newReasoningContractExecutor(t)
			auth := &cliproxyauth.Auth{
				Provider: "openai-compatibility",
				Attributes: map[string]string{
					"base_url": server.URL,
					"api_key":  "test-key",
				},
			}
			request := cliproxyexecutor.Request{
				Model:   "m",
				Payload: []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`),
				Metadata: map[string]any{
					"cliproxy.resolved_api_key_model_info": reasoningContractModelInfo("m", tt.contract),
				},
			}
			options := cliproxyexecutor.Options{
				SourceFormat:   sdktranslator.FormatOpenAI,
				ResponseFormat: sdktranslator.FormatOpenAI,
			}

			resp, errExecute := executor.Execute(context.Background(), auth, request, options)
			if errExecute != nil {
				t.Fatalf("Execute error: %v", errExecute)
			}
			got := ojson.GetBytes(resp.Payload, "choices.0.message."+tt.wantField)
			if tt.wantUnchanged {
				if got.Exists() {
					t.Fatalf("%s must not exist, got %q", tt.wantField, got.String())
				}
				if strings.Contains(string(resp.Payload), `"reasoning_content"`) {
					t.Fatalf("none contract must not inject reasoning fields: %s", resp.Payload)
				}
				return
			}
			if !got.Exists() || got.String() != tt.wantValue {
				t.Fatalf("%s = %q (exists=%v), want %q; payload=%s", tt.wantField, got.String(), got.Exists(), tt.wantValue, resp.Payload)
			}
		})
	}
}

// TestOpenAICompatExecutorReasoningContractStreamNormalizesChat verifies the
// stream path reuses the existing dataPayload pipeline: reasoning deltas gain
// the contract field before the frame is forwarded.
func TestOpenAICompatExecutorReasoningContractStreamNormalizesChat(t *testing.T) {
	sseLines := "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning\":\"chain-\"}}]}\n\n" +
		"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"part\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseLines)
	}))
	t.Cleanup(server.Close)

	executor := newReasoningContractExecutor(t)
	auth := &cliproxyauth.Auth{
		Provider: "openai-compatibility",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test-key",
		},
	}
	request := cliproxyexecutor.Request{
		Model:   "m",
		Payload: []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`),
		Metadata: map[string]any{
			"cliproxy.resolved_api_key_model_info": reasoningContractModelInfo("m", "deepseek"),
		},
	}
	options := cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FormatOpenAI,
		ResponseFormat: sdktranslator.FormatOpenAI,
		Stream:         true,
	}

	result, errStream := executor.ExecuteStream(context.Background(), auth, request, options)
	if errStream != nil {
		t.Fatalf("ExecuteStream error: %v", errStream)
	}
	var filled, preserved bool
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error: %v", chunk.Err)
		}
		reasoningContent := ojson.GetBytes(chunk.Payload, "choices.0.delta.reasoning_content")
		if reasoningContent.Exists() && reasoningContent.String() == "chain-" {
			filled = true
		}
		if ojson.GetBytes(chunk.Payload, "choices.0.delta.reasoning").Exists() {
			preserved = true
		}
	}
	if !filled {
		t.Fatal("stream chunks missing filled reasoning_content")
	}
	if !preserved {
		t.Fatal("stream chunks lost the upstream reasoning field")
	}
}

// TestOpenAICompatExecutorReasoningContractRequestBackfillsHistory verifies the
// request side end to end: a stored session whose assistant turns only carry
// `reasoning` gains reasoning_content before reaching the upstream.
func TestOpenAICompatExecutorReasoningContractRequestBackfillsHistory(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)

	payload := `{"model":"m","reasoning_effort":"low","tools":[{"type":"function","function":{"name":"Read","parameters":{}}}],"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"","reasoning":"legacy-chain","tool_calls":[{"id":"c0","type":"function","function":{"name":"Read","arguments":"{}"}}]},{"role":"tool","content":"r"}]}`

	for _, tt := range []struct {
		name        string
		contract    string
		wantExists  bool
		wantContent string
	}{
		{name: "deepseek contract backfills history", contract: "deepseek", wantExists: true, wantContent: "legacy-chain"},
		{name: "none contract leaves history alone", contract: "", wantExists: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstreamBody = nil
			executor := newReasoningContractExecutor(t)
			auth := &cliproxyauth.Auth{
				Provider: "openai-compatibility",
				Attributes: map[string]string{
					"base_url": server.URL,
					"api_key":  "test-key",
				},
			}
			request := cliproxyexecutor.Request{
				Model:   "m",
				Payload: []byte(payload),
				Metadata: map[string]any{
					"cliproxy.resolved_api_key_model_info": reasoningContractModelInfo("m", tt.contract),
				},
			}
			options := cliproxyexecutor.Options{
				SourceFormat:   sdktranslator.FormatOpenAI,
				ResponseFormat: sdktranslator.FormatOpenAI,
			}

			if _, errExecute := executor.Execute(context.Background(), auth, request, options); errExecute != nil {
				t.Fatalf("Execute error: %v", errExecute)
			}
			assistant := ojson.GetBytes(upstreamBody, "messages.1")
			got := assistant.Get("reasoning_content")
			if got.Exists() != tt.wantExists {
				t.Fatalf("upstream reasoning_content exists = %v, want %v; body=%s", got.Exists(), tt.wantExists, upstreamBody)
			}
			if tt.wantExists && got.String() != tt.wantContent {
				t.Fatalf("upstream reasoning_content = %q, want %q; body=%s", got.String(), tt.wantContent, upstreamBody)
			}
			if tt.wantExists && !assistant.Get("reasoning").Exists() {
				t.Fatalf("source reasoning field must be preserved upstream: %s", upstreamBody)
			}
		})
	}
}

// TestOpenAICompatExecutorReasoningContractAutoResolution checks that the auto
// contract infers from the configured upstream model id bound to the request.
func TestOpenAICompatExecutorReasoningContractAutoResolution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning":"auto-chain"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)

	executor := newReasoningContractExecutor(t)
	auth := &cliproxyauth.Auth{
		Provider: "openai-compatibility",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test-key",
		},
	}
	request := cliproxyexecutor.Request{
		Model:   "m",
		Payload: []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`),
		Metadata: map[string]any{
			"cliproxy.resolved_api_key_model_info": reasoningContractModelInfo("deepseek-v4.1-flash-eco", "auto"),
		},
	}
	options := cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FormatOpenAI,
		ResponseFormat: sdktranslator.FormatOpenAI,
	}
	resp, errExecute := executor.Execute(context.Background(), auth, request, options)
	if errExecute != nil {
		t.Fatalf("Execute error: %v", errExecute)
	}
	got := ojson.GetBytes(resp.Payload, "choices.0.message.reasoning_content")
	if !got.Exists() || got.String() != "auto-chain" {
		t.Fatalf("auto contract reasoning_content = %q (exists=%v), want auto-chain; payload=%s", got.String(), got.Exists(), resp.Payload)
	}
}
