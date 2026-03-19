package anthropic

import (
	"encoding/json"
	"testing"
)

func TestMessagesRequestAllowsStringSystemAndContent(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4.5","stream":false,"system":"system prompt","messages":[{"role":"user","content":"hi"}]}`)

	var req MessagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	if got := string(req.System); got != `"system prompt"` {
		t.Fatalf("unexpected raw system: %s", got)
	}
	if got := string(req.Messages[0].Content); got != `"hi"` {
		t.Fatalf("unexpected raw content: %s", got)
	}
}
