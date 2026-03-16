package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/junie"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// JunieAuthenticator implements the OAuth login flow for JetBrains Junie accounts.
type JunieAuthenticator struct {
	CallbackPort int
}

// NewJunieAuthenticator constructs a Junie authenticator with default settings.
func NewJunieAuthenticator() *JunieAuthenticator {
	return &JunieAuthenticator{CallbackPort: 54547}
}

func (a *JunieAuthenticator) Provider() string {
	return "junie"
}

func (a *JunieAuthenticator) RefreshLead() *time.Duration {
	return new(4 * time.Hour)
}

func (a *JunieAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	callbackPort := a.CallbackPort
	if opts.CallbackPort > 0 {
		callbackPort = opts.CallbackPort
	}

	pkceCodes, err := junie.GeneratePKCECodes()
	if err != nil {
		return nil, fmt.Errorf("junie pkce generation failed: %w", err)
	}

	state, err := misc.GenerateRandomState()
	if err != nil {
		return nil, fmt.Errorf("junie state generation failed: %w", err)
	}

	oauthServer := junie.NewOAuthServer(callbackPort)
	if err = oauthServer.Start(); err != nil {
		if strings.Contains(err.Error(), "already in use") {
			return nil, fmt.Errorf("junie oauth: port %d already in use: %w", callbackPort, err)
		}
		return nil, fmt.Errorf("junie oauth: server start failed: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if stopErr := oauthServer.Stop(stopCtx); stopErr != nil {
			log.Warnf("junie oauth server stop error: %v", stopErr)
		}
	}()

	// Use dynamic port from server after Start() (port 0 gets assigned by OS)
	actualPort := oauthServer.Port()
	redirectURI := fmt.Sprintf("http://localhost:%d/", actualPort)

	authSvc := junie.NewJunieAuth(cfg)

	authURL, err := authSvc.GenerateAuthURL(state, pkceCodes, redirectURI)
	if err != nil {
		return nil, fmt.Errorf("junie authorization url generation failed: %w", err)
	}

	if !opts.NoBrowser {
		fmt.Println("Opening browser for JetBrains Junie authentication")
		if !browser.IsAvailable() {
			log.Warn("No browser available; please open the URL manually")
			util.PrintSSHTunnelInstructions(actualPort)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		} else if err = browser.OpenURL(authURL); err != nil {
			log.Warnf("Failed to open browser automatically: %v", err)
			util.PrintSSHTunnelInstructions(actualPort)
			fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
		}
	} else {
		util.PrintSSHTunnelInstructions(actualPort)
		fmt.Printf("Visit the following URL to continue authentication:\n%s\n", authURL)
	}

	fmt.Println("Waiting for Junie authentication callback...")

	callbackCh := make(chan *junie.OAuthResult, 1)
	callbackErrCh := make(chan error, 1)
	manualDescription := ""

	go func() {
		result, errWait := oauthServer.WaitForCallback(5 * time.Minute)
		if errWait != nil {
			callbackErrCh <- errWait
			return
		}
		callbackCh <- result
	}()

	var result *junie.OAuthResult
	var manualPromptTimer *time.Timer
	var manualPromptC <-chan time.Time
	if opts.Prompt != nil {
		manualPromptTimer = time.NewTimer(15 * time.Second)
		manualPromptC = manualPromptTimer.C
		defer manualPromptTimer.Stop()
	}

waitForCallback:
	for {
		select {
		case result = <-callbackCh:
			break waitForCallback
		case err = <-callbackErrCh:
			if strings.Contains(err.Error(), "timeout") {
				return nil, fmt.Errorf("junie oauth: callback timeout: %w", err)
			}
			return nil, err
		case <-manualPromptC:
			manualPromptC = nil
			if manualPromptTimer != nil {
				manualPromptTimer.Stop()
			}
			select {
			case result = <-callbackCh:
				break waitForCallback
			case err = <-callbackErrCh:
				if strings.Contains(err.Error(), "timeout") {
					return nil, fmt.Errorf("junie oauth: callback timeout: %w", err)
				}
				return nil, err
			default:
			}
			input, errPrompt := opts.Prompt("Paste the Junie callback URL (or press Enter to keep waiting): ")
			if errPrompt != nil {
				return nil, errPrompt
			}
			parsed, errParse := misc.ParseOAuthCallback(input)
			if errParse != nil {
				return nil, errParse
			}
			if parsed == nil {
				continue
			}
			manualDescription = parsed.ErrorDescription
			result = &junie.OAuthResult{
				Code:  parsed.Code,
				State: parsed.State,
				Error: parsed.Error,
			}
			break waitForCallback
		}
	}

	if result.Error != "" {
		return nil, fmt.Errorf("junie oauth error %q: %s (status %d)", result.Error, manualDescription, http.StatusBadRequest)
	}

	if result.State != state {
		log.Errorf("State mismatch: expected %s, got %s", state, result.State)
		return nil, fmt.Errorf("junie oauth: %w", junie.ErrInvalidState)
	}

	log.Debug("Junie authorization code received; exchanging for tokens")
	log.Debugf("Code: %s, State: %s", result.Code[:min(20, len(result.Code))], state)

	authBundle, err := authSvc.ExchangeCodeForTokens(ctx, result.Code, redirectURI, pkceCodes)
	if err != nil {
		log.Errorf("Junie token exchange failed: %v", err)
		return nil, fmt.Errorf("junie oauth: %w: %v", junie.ErrCodeExchangeFailed, err)
	}

	tokenStorage := authSvc.CreateTokenStorage(authBundle)

	if tokenStorage == nil {
		return nil, fmt.Errorf("junie token storage creation failed")
	}

	// Use the bundle's Label as the account identifier for the filename.
	// Fall back to a generic name if no label is available.
	label := authBundle.Label
	if label == "" {
		label = "account"
	}

	fileName := fmt.Sprintf("junie-%s.json", label)
	metadata := map[string]any{
		"label": label,
	}

	fmt.Println("Junie authentication successful")

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Storage:  tokenStorage,
		Metadata: metadata,
	}, nil
}
