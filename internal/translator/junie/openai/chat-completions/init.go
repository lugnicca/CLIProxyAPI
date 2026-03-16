package chat_completions

import (
	"context"

	. "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/translator"
)

// identityRequest is an identity (pass-through) request translator.
// The JunieExecutor sends requests directly in OpenAI format to the
// Ingrazzio /v1/chat/completions endpoint, so no format conversion is needed.
func identityRequest(_ string, rawJSON []byte, _ bool) []byte {
	return rawJSON
}

// identityStream is an identity (pass-through) streaming response translator.
// Ingrazzio returns native OpenAI SSE format, so no translation is needed.
func identityStream(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) []string {
	return []string{string(rawJSON)}
}

// identityNonStream is an identity (pass-through) non-streaming response translator.
// Ingrazzio returns native OpenAI JSON format, so no translation is needed.
func identityNonStream(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) string {
	return string(rawJSON)
}

func init() {
	translator.Register(
		OpenAI,
		Junie,
		identityRequest,
		interfaces.TranslateResponse{
			Stream:    identityStream,
			NonStream: identityNonStream,
		},
	)
}
