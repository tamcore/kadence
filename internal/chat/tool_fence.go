package chat

import (
	"encoding/json"
	"log/slog"

	"github.com/tamcore/kadence/internal/provider"
)

// untrustedToolResultEnvelope carries one tool result as data inside the same
// <untrusted_context> fence attachments and selected documents already use.
type untrustedToolResultEnvelope struct {
	Tool   string `json:"tool"`
	Result string `json:"result"`
}

// fencedToolResultMessage builds the role:"tool" message for a remote tool
// result, wrapping it in the <untrusted_context> fence so the trust boundary is
// structural and not only stated in the system prompt.
//
// The payload is JSON-encoded, and encoding/json escapes "<" and ">", so a
// result that itself contains the fence marker cannot terminate its own fence.
// Fencing must stay the LAST step: the 256 KiB cap in internal/mcp runs before
// the result reaches here, so the closing marker is appended to an already
// truncated payload and can never be cut off.
func fencedToolResultMessage(tc provider.ToolCall, result string) provider.Message {
	message := provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name}
	encoded, err := json.Marshal(untrustedToolResultEnvelope{Tool: tc.Name, Result: result})
	if err != nil {
		// Unreachable for string fields; drop the payload rather than forward an
		// unfenced result.
		slog.Error("encode untrusted tool result", "err", err, "tool", tc.Name)
		message.Content = "error: tool result could not be encoded"
		return message
	}
	message.Content = untrustedContextOpen + "\n" + string(encoded) + "\n" + untrustedContextClose
	return message
}
