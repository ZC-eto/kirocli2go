package openai

import (
	"encoding/json"
	"testing"
)

func TestChatCompletionRequestAllowsStringContent(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4.5","stream":false,"messages":[{"role":"user","content":"hi"}],"max_tokens":16}`)

	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	if got := string(req.Messages[0].Content); got != `"hi"` {
		t.Fatalf("unexpected raw content: %s", got)
	}
}
