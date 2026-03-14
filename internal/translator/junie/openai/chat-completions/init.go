package chat_completions

import (
	"context"

	. "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/translator"
)

// init registers identity (pass-through) translators for the junie provider format.
//
// The JunieExecutor now forwards requests to the Ingrazzio proxy endpoint
// (https://ingrazzio-cloud-prod.labs.jb.gg/v1/chat/completions) which accepts
// native OpenAI Chat Completions format directly. No request/response translation
// is needed — the OpenAI payload is forwarded as-is and the OpenAI response is
// returned as-is.
//
// These identity translators ensure that any code path that looks up junie
// translators in the registry gets the payload unchanged.
func init() {
	translator.Register(
		OpenAI,
		Junie,
		// Request: pass through as-is (OpenAI format forwarded directly to Ingrazzio)
		func(model string, rawJSON []byte, stream bool) []byte {
			return rawJSON
		},
		interfaces.TranslateResponse{
			// Stream: pass each SSE line through unchanged
			Stream: func(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) []string {
				if len(rawJSON) == 0 {
					return nil
				}
				return []string{string(rawJSON)}
			},
			// NonStream: pass the full response through unchanged
			NonStream: func(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) string {
				return string(rawJSON)
			},
		},
	)
}
