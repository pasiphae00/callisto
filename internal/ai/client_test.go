package ai

import (
	"encoding/json"
	"testing"
)

var testActions = []Action{
	{ID: "lido.deposit", Name: "Deposit ETH to Lido", Fields: []Field{{Key: "amount"}}},
	{ID: "weth.wrap", Name: "Wrap ETH", Fields: []Field{{Key: "amount"}}},
}

func TestParseProposeAction(t *testing.T) {
	raw := json.RawMessage(`{"content":[
		{"type":"text","text":"Sure."},
		{"type":"tool_use","name":"propose_action","input":{"action_id":"lido.deposit","params":{"amount":"5"}}}
	]}`)
	r, err := parseResolution(raw, testActions)
	if err != nil {
		t.Fatalf("parseResolution: %v", err)
	}
	if !r.OK || r.ActionID != "lido.deposit" || r.Params["amount"] != "5" {
		t.Fatalf("got %+v", r)
	}
}

func TestParseNumericParamCoerced(t *testing.T) {
	raw := json.RawMessage(`{"content":[
		{"type":"tool_use","name":"propose_action","input":{"action_id":"weth.wrap","params":{"amount":10}}}
	]}`)
	r, _ := parseResolution(raw, testActions)
	if !r.OK || r.Params["amount"] != "10" {
		t.Errorf("numeric coercion failed: %+v", r)
	}
}

func TestParseCannotPrepare(t *testing.T) {
	raw := json.RawMessage(`{"content":[
		{"type":"tool_use","name":"cannot_prepare","input":{"reason":"borrowing is not supported yet"}}
	]}`)
	r, _ := parseResolution(raw, testActions)
	if r.OK || r.Reason != "borrowing is not supported yet" {
		t.Errorf("got %+v", r)
	}
}

func TestParseUnknownActionRejected(t *testing.T) {
	raw := json.RawMessage(`{"content":[
		{"type":"tool_use","name":"propose_action","input":{"action_id":"aave.borrow","params":{}}}
	]}`)
	r, _ := parseResolution(raw, testActions)
	if r.OK {
		t.Error("unknown action should be rejected")
	}
}

func TestBuildRequestShape(t *testing.T) {
	b, err := buildRequest(defaultModel, "stake 5 eth with lido", "Ethereum Mainnet", testActions)
	if err != nil {
		t.Fatal(err)
	}
	var req apiRequest
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatalf("request not valid JSON: %v", err)
	}
	if len(req.Tools) != 2 || req.Messages[0].Content != "stake 5 eth with lido" {
		t.Errorf("unexpected request: tools=%d", len(req.Tools))
	}
	if string(req.ToolChoice) != `{"type":"any"}` {
		t.Errorf("tool_choice = %s", req.ToolChoice)
	}
}
