package helps

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// Reasoning contract values accepted by the openai-compatibility
// reasoning-contract model option.
const (
	// ReasoningContractNone keeps request and response payloads untouched.
	ReasoningContractNone = "none"
	// ReasoningContractAuto infers the contract from the model name.
	ReasoningContractAuto = "auto"
	// ReasoningContractDeepSeek matches the DeepSeek contract: reasoning_content
	// must be passed back in full on every request that carries tools, even for
	// turns without tool calls, otherwise the API returns 400.
	ReasoningContractDeepSeek = "deepseek"
	// ReasoningContractMiniMax matches the MiniMax M3.1 contract: the thinking
	// content rides in reasoning_content and the complete assistant message must
	// be appended to the history to keep the reasoning chain connected.
	ReasoningContractMiniMax = "minimax"
	// ReasoningContractOpenRouter matches the OpenRouter contract: reasoning is
	// the canonical field and reasoning_content is accepted as an alias.
	ReasoningContractOpenRouter = "openrouter"
	// ReasoningContractAnthropic keeps thinking blocks untouched; the Anthropic
	// contract is handled by the Claude thinking translation, not by this layer.
	ReasoningContractAnthropic = "anthropic"
)

// NormalizeReasoningContract validates a configured reasoning-contract value.
// Empty and unknown values fall back to none so that typos never enable edits.
func NormalizeReasoningContract(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ReasoningContractAuto:
		return ReasoningContractAuto
	case ReasoningContractDeepSeek:
		return ReasoningContractDeepSeek
	case ReasoningContractMiniMax:
		return ReasoningContractMiniMax
	case ReasoningContractOpenRouter:
		return ReasoningContractOpenRouter
	case ReasoningContractAnthropic:
		return ReasoningContractAnthropic
	default:
		return ReasoningContractNone
	}
}

// ResolveReasoningContract resolves the auto contract against the model name.
// Unknown model families stay on none.
func ResolveReasoningContract(contract, modelName string) string {
	contract = NormalizeReasoningContract(contract)
	if contract != ReasoningContractAuto {
		return contract
	}
	name := strings.ToLower(modelName)
	switch {
	case containsAnySubstring(name, "deepseek", "kimi", "mimo"):
		return ReasoningContractDeepSeek
	case strings.Contains(name, "minimax"):
		return ReasoningContractMiniMax
	case strings.Contains(name, "openrouter"):
		return ReasoningContractOpenRouter
	case containsAnySubstring(name, "claude", "anthropic"):
		return ReasoningContractAnthropic
	default:
		return ReasoningContractNone
	}
}

func containsAnySubstring(value string, substrings ...string) bool {
	for _, substring := range substrings {
		if strings.Contains(value, substring) {
			return true
		}
	}
	return false
}

// APIKeyModelReasoningContract returns the reasoning contract bound to this
// execution attempt, with auto resolved against the configured upstream model
// name (falling back to the requested model name when unbound). Attempts
// without authoritative model capabilities resolve to none.
func APIKeyModelReasoningContract(req cliproxyexecutor.Request) string {
	modelInfo, ok := cliproxyauth.ResolvedModelInfo(req)
	if !ok || modelInfo == nil {
		return ReasoningContractNone
	}
	model := strings.TrimSpace(modelInfo.ID)
	if model == "" {
		model = strings.TrimSpace(req.Model)
	}
	if baseModel := strings.TrimSpace(thinking.ParseSuffix(model).ModelName); baseModel != "" {
		model = baseModel
	}
	return ResolveReasoningContract(modelInfo.ReasoningContract, model)
}

// reasoningContractEditsResponses reports whether the contract aligns response
// reasoning fields for OpenAI chat clients.
func reasoningContractEditsResponses(contract string) bool {
	switch contract {
	case ReasoningContractDeepSeek, ReasoningContractMiniMax, ReasoningContractOpenRouter:
		return true
	default:
		return false
	}
}

// ReasoningContractTargetField returns the response field the contract fills
// for OpenAI chat clients.
func ReasoningContractTargetField(contract string) string {
	if NormalizeReasoningContract(contract) == ReasoningContractOpenRouter {
		return "reasoning"
	}
	return "reasoning_content"
}

// NormalizeChatCompletionReasoning aligns the reasoning fields of an OpenAI
// chat completion response with the configured reasoning contract. It works on
// both full bodies (choices[].message) and stream chunks (choices[].delta),
// only adds the contract's own field, never overwrites an existing non-blank
// value and never touches reasoning_details. The body is returned unchanged
// when the contract is disabled or nothing matches.
func NormalizeChatCompletionReasoning(body []byte, contract string) ([]byte, error) {
	contract = NormalizeReasoningContract(contract)
	if !reasoningContractEditsResponses(contract) {
		return body, nil
	}
	// Zero-allocation prefilter: without any reasoning key there is nothing to
	// mirror, so skip the document build entirely.
	if !bytes.Contains(body, []byte(`"reasoning`)) {
		return body, nil
	}
	doc := ojson.NewDoc(body)
	if doc.Len() == 0 {
		return body, nil
	}
	choices := doc.Get("choices")
	if !choices.Exists() || !choices.IsArray() {
		return body, nil
	}
	target := ReasoningContractTargetField(contract)
	modified := false
	for idx, choice := range choices.Array() {
		for _, holder := range []string{"delta", "message"} {
			node := choice.Get(holder)
			if !node.Exists() || !node.IsObject() {
				continue
			}
			// MiniMax returns reasoning_details as signature blocks that must be
			// replayed verbatim. Clients read reasoning_content first, so
			// synthesizing one would make them drop the details sequence on the
			// next turn and break the interleaved-thinking chain.
			if contract == ReasoningContractMiniMax && node.Get("reasoning_details").IsArray() {
				continue
			}
			existing := node.Get(target)
			if jsonResultPopulated(existing) {
				continue
			}
			text := reasoningTextOfResult(node, target)
			if text == "" {
				continue
			}
			if err := doc.Set(fmt.Sprintf("choices.%d.%s.%s", idx, holder, target), text); err == nil {
				modified = true
			} else {
				return body, fmt.Errorf("set %s: %w", target, err)
			}
		}
	}
	if !modified {
		return body, nil
	}
	return doc.Bytes(), nil
}

// reasoningTextOfResult extracts reasoning text from a message or delta object
// by trying reasoning_content, reasoning (string or object form) and
// reasoning_details[].text in that order. excludeField names the target field
// that must not act as its own source.
func reasoningTextOfResult(res ojson.Result, excludeField string) string {
	if excludeField != "reasoning_content" {
		if text := jsonStringResult(res.Get("reasoning_content")); text != "" {
			return text
		}
	}
	if excludeField != "reasoning" {
		reasoning := res.Get("reasoning")
		if reasoning.Type == ojson.String {
			if strings.TrimSpace(reasoning.Str) != "" {
				return reasoning.Str
			}
		} else if reasoning.IsObject() {
			if text := jsonStringResult(reasoning.Get("text")); text != "" {
				return text
			}
			if text := jsonStringResult(reasoning.Get("summary")); text != "" {
				return text
			}
		}
	}
	details := res.Get("reasoning_details")
	if details.IsArray() {
		var builder strings.Builder
		for _, item := range details.Array() {
			text := jsonStringResult(item.Get("text"))
			if text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(text)
		}
		if strings.TrimSpace(builder.String()) != "" {
			return builder.String()
		}
	}
	return ""
}

func jsonStringResult(res ojson.Result) string {
	if res.Type == ojson.String && strings.TrimSpace(res.Str) != "" {
		return res.Str
	}
	return ""
}

// shouldReconcileReasoningContract reports whether the request-side reasoning
// fallback must run for this execution attempt. OpenRouter accepts
// reasoning_content as an alias of reasoning, so its request side stays
// untouched; Anthropic thinking blocks never reach this layer.
func shouldReconcileReasoningContract(req cliproxyexecutor.Request, toFormat string) (string, bool) {
	contract := APIKeyModelReasoningContract(req)
	if !reasoningContractEditsResponses(contract) || contract == ReasoningContractOpenRouter {
		return contract, false
	}
	if !strings.EqualFold(strings.TrimSpace(toFormat), "openai") {
		return contract, false
	}
	return contract, true
}

// reconcileReasoningContractDoc backfills reasoning fields on assistant history
// so strict upstreams recognize the reasoning chain in tool-call conversations.
// It scans every assistant message, not only the latest turn, because stored
// sessions may hold reasoning under a spelling the target upstream ignores.
// It never synthesizes reasoning_details and never writes a placeholder for
// the MiniMax contract.
func reconcileReasoningContractDoc(doc *ojson.Doc, contract string) bool {
	if doc == nil || doc.Len() == 0 {
		return false
	}
	messages := doc.Get("messages")
	if !messages.Exists() || !messages.IsArray() {
		return false
	}
	// DeepSeek stitches reasoning_content into the context only for requests
	// that carry tools; without tools the field is ignored, so nothing to fix.
	if contract == ReasoningContractDeepSeek && !hasNonEmptyToolsArray(doc) {
		return false
	}
	reasoningActive := reasoningEffortActive(doc)
	modified := false
	for idx, msg := range messages.Array() {
		if strings.TrimSpace(msg.Get("role").String()) != "assistant" {
			continue
		}
		existing := msg.Get("reasoning_content")
		placeholder := existing.Exists() && existing.Type == ojson.String && existing.Str == " "
		if reasoningContentPopulated(existing) && !placeholder {
			continue
		}
		// Real reasoning text wins over blanks and placeholders regardless of
		// whether thinking is active on this request: tools-carrying DeepSeek
		// requests must carry reasoning_content back no matter what.
		if text := reasoningTextOfResult(msg, "reasoning_content"); text != "" {
			if doc.Set(fmt.Sprintf("messages.%d.reasoning_content", idx), text) == nil {
				modified = true
			}
			continue
		}
		if !reasoningActive {
			// Mirror the Claude compat reconcile: drop blank leftovers and
			// stale placeholders when thinking is off; keep real reasoning.
			if existing.Exists() && existing.Type == ojson.String && strings.TrimSpace(existing.Str) == "" {
				if doc.Delete(fmt.Sprintf("messages.%d.reasoning_content", idx)) == nil {
					modified = true
				}
			}
			continue
		}
		if placeholder {
			continue
		}
		// Last resort for strict DeepSeek-style upstreams: a single space
		// placeholder survives signature validation without polluting context.
		// Only the tools-gated DeepSeek contract reaches this branch.
		if contract == ReasoningContractDeepSeek {
			if doc.Set(fmt.Sprintf("messages.%d.reasoning_content", idx), " ") == nil {
				modified = true
			}
		}
	}
	return modified
}

// reasoningContentPopulated reports whether the existing reasoning_content value
// carries real content. Blank strings, nulls and the single-space placeholder do
// not count as populated. Non-string values count as populated and are never
// overwritten.
func reasoningContentPopulated(res ojson.Result) bool {
	if !res.Exists() || res.Type == ojson.Null {
		return false
	}
	if res.Type != ojson.String {
		return true
	}
	return strings.TrimSpace(res.Str) != ""
}

// jsonResultPopulated reports whether a JSON value carries usable content.
// Blank strings and nulls count as empty; everything else counts as populated.
func jsonResultPopulated(res ojson.Result) bool {
	if !res.Exists() {
		return false
	}
	switch res.Type {
	case ojson.String:
		return strings.TrimSpace(res.Str) != ""
	case ojson.Null:
		return false
	default:
		return true
	}
}

func hasNonEmptyToolsArray(doc *ojson.Doc) bool {
	tools := doc.Get("tools")
	return tools.Exists() && tools.IsArray() && len(tools.Array()) > 0
}

// reasoningEffortActive reports whether the request activates thinking through
// a non-disabled reasoning_effort value.
func reasoningEffortActive(doc *ojson.Doc) bool {
	effortRes := doc.Get("reasoning_effort")
	if !effortRes.Exists() {
		return false
	}
	resolvedEffort := strings.ToLower(strings.TrimSpace(effortRes.String()))
	return resolvedEffort != "" && resolvedEffort != string(thinking.LevelNone) && resolvedEffort != "0" && resolvedEffort != "false"
}
