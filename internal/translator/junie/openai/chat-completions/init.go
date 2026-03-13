package chat_completions

import (
	. "github.com/router-for-me/CLIProxyAPI/v6/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/translator/translator"
)

func init() {
	translator.Register(
		OpenAI,
		Junie,
		ConvertOpenAIRequestToJunie,
		interfaces.TranslateResponse{
			Stream:    ConvertJunieResponseToOpenAI,
			NonStream: ConvertJunieResponseToOpenAINonStream,
		},
	)
}
