package helps

import (
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestNormalizeReasoningContract(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"", ReasoningContractNone},
		{"none", ReasoningContractNone},
		{"unknown-value", ReasoningContractNone},
		{"deepseek", ReasoningContractDeepSeek},
		{" DeepSeek ", ReasoningContractDeepSeek},
		{"minimax", ReasoningContractMiniMax},
		{"openrouter", ReasoningContractOpenRouter},
		{"anthropic", ReasoningContractAnthropic},
		{"auto", ReasoningContractAuto},
	} {
		if got := NormalizeReasoningContract(tc.raw); got != tc.want {
			t.Fatalf("NormalizeReasoningContract(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestResolveReasoningContractAutoInference(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  string
	}{
		{"deepseek-v4.1-flash-eco", ReasoningContractDeepSeek},
		{"kimi-k2", ReasoningContractDeepSeek},
		{"mimo-x", ReasoningContractDeepSeek},
		{"MiniMax-M3.1-Flash", ReasoningContractMiniMax},
		{"vendor/openrouter/mix", ReasoningContractOpenRouter},
		{"claude-sonnet-5", ReasoningContractAnthropic},
		{"gpt-6", ReasoningContractNone},
	} {
		if got := ResolveReasoningContract(ReasoningContractAuto, tc.model); got != tc.want {
			t.Fatalf("ResolveReasoningContract(auto, %q) = %q, want %q", tc.model, got, tc.want)
		}
	}
	// Explicit values beat auto inference.
	if got := ResolveReasoningContract(ReasoningContractOpenRouter, "deepseek-v4"); got != ReasoningContractOpenRouter {
		t.Fatalf("explicit openrouter = %q, want %q", got, ReasoningContractOpenRouter)
	}
}

func TestAPIKeyModelReasoningContract(t *testing.T) {
	req := cliproxyexecutor.Request{
		Model: "deepseek-alias(high)",
		Metadata: map[string]any{
			"cliproxy.resolved_api_key_model_info": &registry.ModelInfo{
				ID:                "deepseek-v4.1-flash-eco",
				ReasoningContract: "auto",
			},
		},
	}
	if got := APIKeyModelReasoningContract(req); got != ReasoningContractDeepSeek {
		t.Fatalf("auto contract with deepseek alias = %q, want %q", got, ReasoningContractDeepSeek)
	}

	req.Metadata["cliproxy.resolved_api_key_model_info"] = &registry.ModelInfo{ID: "m", ReasoningContract: "nope"}
	if got := APIKeyModelReasoningContract(req); got != ReasoningContractNone {
		t.Fatalf("unknown contract = %q, want %q", got, ReasoningContractNone)
	}

	if got := APIKeyModelReasoningContract(cliproxyexecutor.Request{}); got != ReasoningContractNone {
		t.Fatalf("missing model info = %q, want %q", got, ReasoningContractNone)
	}
}

func TestNormalizeChatCompletionReasoning(t *testing.T) {
	for _, tc := range []struct {
		name            string
		contract        string
		body            string
		wantUnchanged   bool
		wantField       string
		wantValue       string
		wantDetailsSent string
	}{
		{
			name:      "deepseek fills reasoning_content from reasoning",
			contract:  ReasoningContractDeepSeek,
			body:      `{"choices":[{"message":{"role":"assistant","content":"hi","reasoning":"chain here"}}]}`,
			wantField: "reasoning_content", wantValue: "chain here",
		},
		{
			name:      "deepseek fills stream delta",
			contract:  ReasoningContractDeepSeek,
			body:      `{"choices":[{"delta":{"reasoning":"partial chain"}}]}`,
			wantField: "reasoning_content", wantValue: "partial chain",
		},
		{
			name:      "deepseek keeps existing reasoning_content",
			contract:  ReasoningContractDeepSeek,
			body:      `{"choices":[{"message":{"reasoning":"other","reasoning_content":"native"}}]}`,
			wantField: "reasoning_content", wantValue: "native",
		},
		{
			name:          "deepseek leaves native response byte-identical",
			contract:      ReasoningContractDeepSeek,
			body:          `{"choices":[{"message":{"reasoning_content":"native","content":"hi"}}]}`,
			wantUnchanged: true,
		},
		{
			name:          "deepseek never writes a placeholder on responses",
			contract:      ReasoningContractDeepSeek,
			body:          `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`,
			wantUnchanged: true,
		},
		{
			name:      "deepseek fills from reasoning_details without touching them",
			contract:  ReasoningContractDeepSeek,
			body:      `{"choices":[{"message":{"reasoning_details":[{"type":"reasoning.text","text":"d0","format":"unknown","signature":null},{"type":"reasoning.text","text":"d1"}]}}]}`,
			wantField: "reasoning_content", wantValue: "d0\nd1",
		},
		{
			name:      "deepseek fills from reasoning object text",
			contract:  ReasoningContractDeepSeek,
			body:      `{"choices":[{"message":{"reasoning":{"text":"obj text"}}}]}`,
			wantField: "reasoning_content", wantValue: "obj text",
		},
		{
			name:      "deepseek treats null and blank fields as missing",
			contract:  ReasoningContractDeepSeek,
			body:      `{"choices":[{"message":{"reasoning_content":null,"reasoning":""}}]}`,
			wantField: "reasoning_content", wantValue: "",
			wantUnchanged: true,
		},
		{
			name:      "openrouter fills reasoning from reasoning_content",
			contract:  ReasoningContractOpenRouter,
			body:      `{"choices":[{"message":{"reasoning_content":"deepseek style"}}]}`,
			wantField: "reasoning", wantValue: "deepseek style",
		},
		{
			name:      "openrouter keeps existing reasoning and details",
			contract:  ReasoningContractOpenRouter,
			body:      `{"choices":[{"message":{"reasoning":"or","reasoning_details":[{"type":"reasoning.text","text":"t","signature":"sig"}]}}]}`,
			wantField: "reasoning", wantValue: "or",
		},
		{
			name:      "minimax fills reasoning_content like deepseek",
			contract:  ReasoningContractMiniMax,
			body:      `{"choices":[{"message":{"reasoning":"mm"}}]}`,
			wantField: "reasoning_content", wantValue: "mm",
		},
		{
			// Gateways that only expose rewritten summaries return
			// reasoning.summary entries instead of reasoning.text.
			name:      "deepseek reads reasoning_details summary entries",
			contract:  ReasoningContractDeepSeek,
			body:      `{"choices":[{"message":{"reasoning_details":[{"index":0,"type":"reasoning.summary","summary":"sum-a"},{"index":1,"type":"reasoning.encrypted","data":"enc"}]}}]}`,
			wantField: "reasoning_content", wantValue: "sum-a",
		},
		{
			// MiniMax replays reasoning_details verbatim; synthesizing
			// reasoning_content would make clients drop the details chain.
			name:          "minimax skips synthesis when reasoning_details present",
			contract:      ReasoningContractMiniMax,
			body:          `{"choices":[{"message":{"reasoning_details":[{"type":"reasoning.text","text":"sig-chain","signature":"sig"}]}}]}`,
			wantUnchanged: true,
		},
		{
			name:          "none and anthropic contracts stay untouched",
			contract:      ReasoningContractAnthropic,
			body:          `{"choices":[{"message":{"reasoning":"r"}}]}`,
			wantUnchanged: true,
		},
		{
			name:          "usage frame without choices is skipped",
			contract:      ReasoningContractDeepSeek,
			body:          `{"usage":{"prompt_tokens":1}}`,
			wantUnchanged: true,
		},
		{
			name:          "error payload without choices is skipped",
			contract:      ReasoningContractDeepSeek,
			body:          `{"error":{"message":"boom"}}`,
			wantUnchanged: true,
		},
		{
			name:          "body without reasoning keys skips parsing",
			contract:      ReasoningContractDeepSeek,
			body:          `{"choices":[{"message":{"role":"assistant","content":"plain"}}]}`,
			wantUnchanged: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := NormalizeChatCompletionReasoning([]byte(tc.body), tc.contract)
			if err != nil {
				t.Fatalf("NormalizeChatCompletionReasoning() error = %v", err)
			}
			got := ojson.GetBytes(out, "choices.0.message."+tc.wantField)
			gotDelta := ojson.GetBytes(out, "choices.0.delta."+tc.wantField)
			field := got
			if !field.Exists() {
				field = gotDelta
			}
			if tc.wantUnchanged {
				if string(out) != tc.body {
					t.Fatalf("body changed:\n got  %s\n want %s", out, tc.body)
				}
				return
			}
			if !field.Exists() || field.String() != tc.wantValue {
				t.Fatalf("%s = %q (exists=%v), want %q; out=%s", tc.wantField, field.String(), field.Exists(), tc.wantValue, out)
			}
			if tc.wantDetailsSent != "" {
				details := ojson.GetBytes(out, "choices.0.message.reasoning_details")
				if details.String() != tc.wantDetailsSent {
					t.Fatalf("reasoning_details = %s, want %s", details.String(), tc.wantDetailsSent)
				}
			}
		})
	}
}

func TestNormalizeChatCompletionReasoningPreservesDetailsAndIsIdempotent(t *testing.T) {
	body := `{"choices":[{"message":{"reasoning_content":"c","reasoning_details":[{"type":"reasoning.text","text":"t","format":"unknown","signature":null}]}}]}`
	first, err := NormalizeChatCompletionReasoning([]byte(body), ReasoningContractOpenRouter)
	if err != nil {
		t.Fatalf("first normalize error = %v", err)
	}
	second, err := NormalizeChatCompletionReasoning(first, ReasoningContractOpenRouter)
	if err != nil {
		t.Fatalf("second normalize error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("normalization is not idempotent:\n first  %s\n second %s", first, second)
	}
	details := ojson.GetBytes(second, "choices.0.message.reasoning_details")
	if details.String() != `[{"type":"reasoning.text","text":"t","format":"unknown","signature":null}]` {
		t.Fatalf("reasoning_details modified: %s", details.String())
	}
}

// contractReconcileRequest builds a request bound to an API-key model info with
// the given reasoning contract.
func contractReconcileRequest(contract string) cliproxyexecutor.Request {
	return cliproxyexecutor.Request{
		Model: "m",
		Metadata: map[string]any{
			"cliproxy.resolved_api_key_model_info": &registry.ModelInfo{
				ID:                "m",
				ReasoningContract: contract,
				Thinking:          &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}},
			},
		},
	}
}

func TestReconcileReasoningContractDocBackfillsHistory(t *testing.T) {
	// Stored-session shape: every assistant turn carries only `reasoning`.
	doc := ojson.NewDocString(`{"model":"m","reasoning_effort":"low","tools":[{"type":"function","function":{"name":"Read"}}],"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"","reasoning":"thought-0","tool_calls":[{"id":"c0","type":"function","function":{"name":"Read","arguments":"{}"}}]},{"role":"tool","content":"r"},{"role":"assistant","content":"","reasoning":"thought-1"}]}`)
	if !reconcileReasoningContractDoc(doc, ReasoningContractDeepSeek) {
		t.Fatal("reconcile reported no modification")
	}
	out := doc.String()
	for _, want := range []string{`"reasoning_content":"thought-0"`, `"reasoning_content":"thought-1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
	if !strings.Contains(out, `"reasoning":"thought-0"`) {
		t.Fatalf("source reasoning field must be preserved: %s", out)
	}
}

func TestReconcileReasoningContractDocTable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		contract      string
		toFormat      string
		body          string
		wantField     string // message index.field to inspect
		wantExists    bool
		wantValue     string
		wantUnchanged bool
	}{
		{
			name:      "deepseek missing field with tools and active thinking gets placeholder",
			contract:  ReasoningContractDeepSeek,
			toFormat:  "openai",
			body:      `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","tool_calls":[{"id":"c","type":"function","function":{"name":"f"}}]}]}`,
			wantField: "messages.0.reasoning_content", wantExists: true, wantValue: " ",
		},
		{
			name:          "deepseek without tools is skipped entirely",
			contract:      ReasoningContractDeepSeek,
			toFormat:      "openai",
			body:          `{"reasoning_effort":"low","messages":[{"role":"assistant","content":"","tool_calls":[{"id":"c","type":"function","function":{"name":"f"}}]}]}`,
			wantUnchanged: true,
		},
		{
			name:          "deepseek with empty tools array is skipped",
			contract:      ReasoningContractDeepSeek,
			toFormat:      "openai",
			body:          `{"reasoning_effort":"low","tools":[],"messages":[{"role":"assistant","content":"","tool_calls":[{"id":"c","type":"function","function":{"name":"f"}}]}]}`,
			wantUnchanged: true,
		},
		{
			name:      "deepseek blank field with reasoning text gets real text",
			contract:  ReasoningContractDeepSeek,
			toFormat:  "openai",
			body:      `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning":"real","reasoning_content":""}]}`,
			wantField: "messages.0.reasoning_content", wantExists: true, wantValue: "real",
		},
		{
			name:      "deepseek placeholder replaced by real reasoning text",
			contract:  ReasoningContractDeepSeek,
			toFormat:  "openai",
			body:      `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning":"real","reasoning_content":" "}]}`,
			wantField: "messages.0.reasoning_content", wantExists: true, wantValue: "real",
		},
		{
			name:      "deepseek real reasoning_content is never overwritten",
			contract:  ReasoningContractDeepSeek,
			toFormat:  "openai",
			body:      `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning":"other","reasoning_content":"native"}]}`,
			wantField: "messages.0.reasoning_content", wantExists: true, wantValue: "native",
		},
		{
			name:      "deepseek existing placeholder survives when thinking is active",
			contract:  ReasoningContractDeepSeek,
			toFormat:  "openai",
			body:      `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning_content":" "}]}`,
			wantField: "messages.0.reasoning_content", wantExists: true, wantValue: " ",
		},
		{
			name:          "deepseek placeholder removed when thinking is off",
			contract:      ReasoningContractDeepSeek,
			toFormat:      "openai",
			body:          `{"reasoning_effort":"none","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning_content":" ","tool_calls":[{"id":"c","type":"function","function":{"name":"f"}}]}]}`,
			wantField:     "messages.0.reasoning_content",
			wantExists:    false,
			wantUnchanged: false,
		},
		{
			name:          "deepseek blank field removed when thinking is off",
			contract:      ReasoningContractDeepSeek,
			toFormat:      "openai",
			body:          `{"tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning_content":""}]}`,
			wantField:     "messages.0.reasoning_content",
			wantExists:    false,
			wantUnchanged: false,
		},
		{
			name:      "deepseek fills history text even when thinking is off",
			contract:  ReasoningContractDeepSeek,
			toFormat:  "openai",
			body:      `{"tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning":"kept"}]}`,
			wantField: "messages.0.reasoning_content", wantExists: true, wantValue: "kept",
		},
		{
			name:          "minimax fills without tools gate and never writes placeholders",
			contract:      ReasoningContractMiniMax,
			toFormat:      "openai",
			body:          `{"reasoning_effort":"low","messages":[{"role":"assistant","content":"","reasoning":"mm"},{"role":"assistant","content":"","tool_calls":[{"id":"c","type":"function","function":{"name":"f"}}]}]}`,
			wantField:     "messages.1.reasoning_content",
			wantExists:    false,
			wantUnchanged: false,
		},
		{
			name:          "openrouter request side stays untouched",
			contract:      ReasoningContractOpenRouter,
			toFormat:      "openai",
			body:          `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning_content":"alias"}]}`,
			wantUnchanged: true,
		},
		{
			name:          "anthropic contract stays untouched",
			contract:      ReasoningContractAnthropic,
			toFormat:      "openai",
			body:          `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning":"r"}]}`,
			wantUnchanged: true,
		},
		{
			name:          "responses compact target format is skipped",
			contract:      ReasoningContractDeepSeek,
			toFormat:      "openai-response",
			body:          `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"assistant","content":"","reasoning":"r"}]}`,
			wantUnchanged: true,
		},
		{
			name:          "non assistant messages are untouched",
			contract:      ReasoningContractDeepSeek,
			toFormat:      "openai",
			body:          `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":[{"role":"user","content":"","reasoning":"u"},{"role":"tool","content":"","reasoning":"t"}]}`,
			wantUnchanged: true,
		},
		{
			name:          "messages missing or non-array is skipped",
			contract:      ReasoningContractDeepSeek,
			toFormat:      "openai",
			body:          `{"reasoning_effort":"low","tools":[{"type":"function"}],"messages":{"role":"assistant"}}`,
			wantUnchanged: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantUnchanged && tc.wantField != "" && tc.wantValue != "" {
				t.Fatalf("test %q misconfigured", tc.name)
			}
			doc := ojson.NewDocString(tc.body)
			before := doc.String()
			// Mirror the production wiring: the gate decides whether the
			// reconcile runs for this target format and contract.
			if contract, ok := shouldReconcileReasoningContract(contractReconcileRequest(tc.contract), tc.toFormat); ok {
				reconcileReasoningContractDoc(doc, contract)
			}
			got := doc.Get(tc.wantField)
			if got.Exists() != tc.wantExists {
				t.Fatalf("%s exists = %v, want %v; doc=%s", tc.wantField, got.Exists(), tc.wantExists, doc.String())
			}
			if tc.wantExists && got.String() != tc.wantValue {
				t.Fatalf("%s = %q, want %q; doc=%s", tc.wantField, got.String(), tc.wantValue, doc.String())
			}
			if tc.wantUnchanged && doc.String() != before {
				t.Fatalf("doc changed:\n got  %s\n want %s", doc.String(), before)
			}
		})
	}
}

func TestShouldReconcileReasoningContractGate(t *testing.T) {
	req := contractReconcileRequest(ReasoningContractDeepSeek)
	if _, ok := shouldReconcileReasoningContract(req, "openai"); !ok {
		t.Fatal("deepseek contract with openai target should reconcile")
	}
	if _, ok := shouldReconcileReasoningContract(req, "claude"); ok {
		t.Fatal("deepseek contract with claude target should not reconcile")
	}
	reqOpenRouter := contractReconcileRequest(ReasoningContractOpenRouter)
	if _, ok := shouldReconcileReasoningContract(reqOpenRouter, "openai"); ok {
		t.Fatal("openrouter contract request side should not reconcile")
	}
	reqNone := contractReconcileRequest(ReasoningContractNone)
	if _, ok := shouldReconcileReasoningContract(reqNone, "openai"); ok {
		t.Fatal("none contract should not reconcile")
	}
}

func TestApplyRequestThinkingDocWiresContractReconcile(t *testing.T) {
	translated := `{"model":"m","reasoning_effort":"low","tools":[{"type":"function","function":{"name":"Read"}}],"messages":[{"role":"user","content":"q"},{"role":"assistant","content":"","reasoning":"legacy-chain","tool_calls":[{"id":"c0","type":"function","function":{"name":"Read","arguments":"{}"}}]}]}`
	req := cliproxyexecutor.Request{
		Model:    "m",
		Payload:  []byte(translated),
		Metadata: map[string]any{"cliproxy.resolved_api_key_model_info": contractReconcileRequest(ReasoningContractDeepSeek).Metadata["cliproxy.resolved_api_key_model_info"]},
	}
	opts := cliproxyexecutor.Options{OriginalRequest: []byte(translated)}
	doc := ojson.NewDocString(translated)
	if err := ApplyRequestThinkingDoc(doc, req, opts, "openai", "openai", "openai-compatibility"); err != nil {
		t.Fatalf("ApplyRequestThinkingDoc() error = %v", err)
	}
	got := doc.Get("messages.1.reasoning_content")
	if !got.Exists() || got.String() != "legacy-chain" {
		t.Fatalf("reasoning_content = %q (exists=%v), want legacy-chain; doc=%s", got.String(), got.Exists(), doc.String())
	}

	// Without the contract the same request stays untouched.
	reqNone := contractReconcileRequest(ReasoningContractNone)
	docNone := ojson.NewDocString(translated)
	if err := ApplyRequestThinkingDoc(docNone, cliproxyexecutor.Request{Model: "m", Payload: []byte(translated), Metadata: reqNone.Metadata}, opts, "openai", "openai", "openai-compatibility"); err != nil {
		t.Fatalf("ApplyRequestThinkingDoc() error = %v", err)
	}
	if docNone.Get("messages.1.reasoning_content").Exists() {
		t.Fatalf("none contract must not add reasoning_content: %s", docNone.String())
	}
}
