// Package ai is Callisto's optional, default-off Claude integration for advanced
// transaction preparation. It maps a natural-language intent onto one of the curated
// registry actions (internal/actions) and its parameters — it never produces calldata.
//
// The Anthropic Messages API is called directly over HTTPS (no SDK), matching the
// from-scratch ethos of internal/walletconnect and keeping the dependency surface
// minimal. A Client is constructed only when the user has enabled AI and set a key, so
// the whole path is inert (cold) until then.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	apiURL         = "https://api.anthropic.com/v1/messages"
	apiVersion     = "2023-06-01"
	defaultModel   = "claude-sonnet-5"
	defaultMaxToks = 1024
)

// Action is the minimal description of a registry action given to the model.
type Action struct {
	ID     string
	Name   string
	Desc   string
	Fields []Field
}

// Field is one action input the model may fill.
type Field struct {
	Key   string
	Label string
	Hint  string
}

// Resolution is the model's structured output: a chosen action + params, or a reason
// it could not map the intent.
type Resolution struct {
	OK       bool
	ActionID string
	Params   map[string]string
	Reason   string // set when !OK
}

// Client calls the Anthropic Messages API with a user-supplied key.
type Client struct {
	apiKey string
	model  string
	http   *http.Client
}

// NewClient builds a client for the given API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		model:  defaultModel,
		http:   &http.Client{Timeout: 60 * time.Second},
	}
}

// Resolve maps intent to one of actions (on chainName), returning the chosen action +
// params or a reason. The model is constrained (forced tool use + explicit rules) to
// pick only from the provided actions; the caller still validates and builds the call
// deterministically.
func (c *Client) Resolve(ctx context.Context, intent, chainName string, actions []Action) (Resolution, error) {
	if strings.TrimSpace(intent) == "" {
		return Resolution{}, fmt.Errorf("empty intent")
	}
	reqBody, err := buildRequest(c.model, intent, chainName, actions)
	if err != nil {
		return Resolution{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return Resolution{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return Resolution{}, err
	}
	defer resp.Body.Close()

	var raw json.RawMessage
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&raw); err != nil {
		return Resolution{}, fmt.Errorf("ai: decode response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Resolution{}, fmt.Errorf("ai: %s", apiErrorMessage(raw, resp.StatusCode))
	}
	return parseResolution(raw, actions)
}

// --- request / response shapes ---------------------------------------------

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type apiRequest struct {
	Model      string          `json:"model"`
	MaxTokens  int             `json:"max_tokens"`
	System     string          `json:"system"`
	Tools      []apiTool       `json:"tools"`
	ToolChoice json.RawMessage `json:"tool_choice"`
	Messages   []apiMessage    `json:"messages"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// buildRequest assembles the Messages API request: a system prompt with the rules +
// the available actions, two tools (propose_action / cannot_prepare), and forced tool
// use so the model must return structured output.
func buildRequest(model, intent, chainName string, actions []Action) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("You translate a user's on-chain intent into exactly ONE of the available actions and its parameters. ")
	sb.WriteString("You may ONLY choose an action from the list below — never invent an action, contract, or parameter. ")
	sb.WriteString("Amounts are in human units (e.g. \"10\" for 10 ETH), as plain decimal strings. ")
	sb.WriteString("If the intent does not clearly map to one available action, call cannot_prepare with a short reason. ")
	sb.WriteString("Do not guess amounts the user did not give.\n\n")
	fmt.Fprintf(&sb, "Connected network: %s.\n\nAvailable actions:\n", chainName)
	for _, a := range actions {
		fmt.Fprintf(&sb, "- id=%q  name=%q  — %s\n", a.ID, a.Name, a.Desc)
		for _, f := range a.Fields {
			fmt.Fprintf(&sb, "    param key=%q  (%s)\n", f.Key, f.Label)
		}
	}

	proposeSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action_id": {"type": "string", "description": "id of the chosen action; must be one of the available ids"},
			"params": {"type": "object", "description": "parameter values keyed by the action's param keys; amounts are decimal strings in human units"}
		},
		"required": ["action_id", "params"]
	}`)
	cannotSchema := json.RawMessage(`{
		"type": "object",
		"properties": {"reason": {"type": "string", "description": "short reason the intent can't be prepared"}},
		"required": ["reason"]
	}`)

	req := apiRequest{
		Model:     model,
		MaxTokens: defaultMaxToks,
		System:    sb.String(),
		Tools: []apiTool{
			{Name: "propose_action", Description: "Select the action and parameters that fulfil the intent.", InputSchema: proposeSchema},
			{Name: "cannot_prepare", Description: "The intent cannot be mapped to an available action.", InputSchema: cannotSchema},
		},
		ToolChoice: json.RawMessage(`{"type":"any"}`),
		Messages:   []apiMessage{{Role: "user", Content: intent}},
	}
	return json.Marshal(req)
}

// parseResolution extracts the tool call from a successful response.
func parseResolution(raw json.RawMessage, actions []Action) (Resolution, error) {
	var body struct {
		Content []apiContentBlock `json:"content"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return Resolution{}, fmt.Errorf("ai: parse content: %w", err)
	}
	for _, b := range body.Content {
		if b.Type != "tool_use" {
			continue
		}
		switch b.Name {
		case "cannot_prepare":
			var in struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(b.Input, &in)
			return Resolution{OK: false, Reason: firstNonEmpty(in.Reason, "the request could not be prepared")}, nil
		case "propose_action":
			var in struct {
				ActionID string                 `json:"action_id"`
				Params   map[string]interface{} `json:"params"`
			}
			if err := json.Unmarshal(b.Input, &in); err != nil {
				return Resolution{}, fmt.Errorf("ai: parse proposal: %w", err)
			}
			if !hasAction(actions, in.ActionID) {
				return Resolution{OK: false, Reason: "the model chose an unknown action"}, nil
			}
			return Resolution{OK: true, ActionID: in.ActionID, Params: stringifyParams(in.Params)}, nil
		}
	}
	// No tool call (e.g. plain text) — surface any text as the reason.
	for _, b := range body.Content {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			return Resolution{OK: false, Reason: strings.TrimSpace(b.Text)}, nil
		}
	}
	return Resolution{OK: false, Reason: "no proposal returned"}, nil
}

func hasAction(actions []Action, id string) bool {
	for _, a := range actions {
		if a.ID == id {
			return true
		}
	}
	return false
}

func stringifyParams(in map[string]interface{}) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = trimFloat(t)
		case json.Number:
			out[k] = t.String()
		default:
			out[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// apiErrorMessage extracts a human message from an Anthropic error response.
func apiErrorMessage(raw json.RawMessage, status int) string {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return fmt.Sprintf("%s (%s)", e.Error.Message, e.Error.Type)
	}
	return fmt.Sprintf("request failed with status %d", status)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
