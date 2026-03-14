// Package junie provides authentication and token management functionality
// for JetBrains Junie AI services. It handles JWT and OAuth token storage,
// serialization, and retrieval for maintaining authenticated sessions with
// the Grazie API and JetBrains Account OAuth.
package junie

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// PostLoginURL is the URL that the OAuth provider redirects to after a successful login.
// The local callback server redirects the browser here after capturing the auth code.
const PostLoginURL = "https://junie.jetbrains.com/cli-auth-complete"

// OAuthServer handles the local HTTP server for OAuth callbacks from JetBrains Account.
// It listens on a dynamically assigned port for the authorization code response
// and captures the necessary parameters to complete the PKCE authentication flow.
type OAuthServer struct {
	// server is the underlying HTTP server instance
	server *http.Server
	// port is the actual port number on which the server is listening (assigned dynamically)
	port int
	// listener is the TCP listener used for dynamic port assignment
	listener net.Listener
	// resultChan is a channel for sending OAuth results
	resultChan chan *OAuthResult
	// errorChan is a channel for sending OAuth errors
	errorChan chan error
	// mu is a mutex for protecting server state
	mu sync.Mutex
	// running indicates whether the server is currently running
	running bool
}

// OAuthResult contains the result of the OAuth callback.
// It holds either the authorization code and state for successful authentication
// or an error message if the authentication failed.
type OAuthResult struct {
	// Code is the authorization code received from the JetBrains OAuth provider
	Code string
	// State is the state parameter used to prevent CSRF attacks
	State string
	// Error contains any error message if the OAuth flow failed
	Error string
}

// NewOAuthServer creates a new OAuth callback server.
// Pass port=0 for dynamic port assignment (required for Junie's dynamic redirect URI).
//
// Parameters:
//   - port: The port number on which the server should listen (0 for dynamic assignment)
//
// Returns:
//   - *OAuthServer: A new OAuthServer instance
func NewOAuthServer(port int) *OAuthServer {
	return &OAuthServer{
		port:       port,
		resultChan: make(chan *OAuthResult, 1),
		errorChan:  make(chan error, 1),
	}
}

// Port returns the actual port on which the server is (or will be) listening.
// Call this after Start() to get the dynamically assigned port.
func (s *OAuthServer) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Start starts the OAuth callback server.
// It sets up the HTTP handler for the root path (/) and the /callback path,
// and begins listening on the configured port (dynamic if port=0).
//
// Returns:
//   - error: An error if the server fails to start
func (s *OAuthServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server is already running")
	}

	// Bind a listener - port 0 lets the OS assign a free port
	addr := fmt.Sprintf(":%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrServerStartFailed, err)
	}

	// Capture the actual assigned port (important when port=0)
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.listener = ln

	mux := http.NewServeMux()
	// Handle root path - JetBrains redirects to http://localhost:<port>/
	mux.HandleFunc("/", s.handleCallback)
	// Also handle /callback for compatibility
	mux.HandleFunc("/callback", s.handleCallback)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	s.running = true

	// Start server in goroutine
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errorChan <- fmt.Errorf("OAuth callback server error: %w", err)
		}
	}()

	return nil
}

// Stop gracefully stops the OAuth callback server.
// It performs a graceful shutdown of the HTTP server with a timeout.
//
// Parameters:
//   - ctx: The context for controlling the shutdown process
//
// Returns:
//   - error: An error if the server fails to stop gracefully
func (s *OAuthServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.server == nil {
		return nil
	}

	log.Debug("Stopping Junie OAuth callback server")

	// Create a context with timeout for shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := s.server.Shutdown(shutdownCtx)
	s.running = false
	s.server = nil
	s.listener = nil

	return err
}

// WaitForCallback waits for the OAuth callback with a timeout.
// It blocks until either an OAuth result is received, an error occurs,
// or the specified timeout is reached.
//
// Parameters:
//   - timeout: The maximum time to wait for the callback
//
// Returns:
//   - *OAuthResult: The OAuth result if successful
//   - error: An error if the callback times out or an error occurs
func (s *OAuthServer) WaitForCallback(timeout time.Duration) (*OAuthResult, error) {
	select {
	case result := <-s.resultChan:
		return result, nil
	case err := <-s.errorChan:
		return nil, err
	case <-time.After(timeout):
		return nil, ErrCallbackTimeout
	}
}

// handleCallback handles the OAuth callback endpoint.
// JetBrains redirects to http://localhost:<port>/?code=...&state=...
// It extracts the authorization code and state from the URL query parameters,
// validates them, and sends the result to the waiting channel.
//
// Parameters:
//   - w: The HTTP response writer
//   - r: The HTTP request
func (s *OAuthServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	log.Debug("Received Junie OAuth callback")

	// Validate request method
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract parameters from query string
	query := r.URL.Query()
	code := query.Get("code")
	state := query.Get("state")
	errorParam := query.Get("error")

	// Handle OAuth error response
	if errorParam != "" {
		log.Errorf("Junie OAuth error received: %s", errorParam)
		result := &OAuthResult{
			Error: errorParam,
		}
		s.sendResult(result)
		http.Error(w, fmt.Sprintf("OAuth error: %s", errorParam), http.StatusBadRequest)
		return
	}

	if code == "" {
		log.Error("No authorization code received in Junie OAuth callback")
		result := &OAuthResult{
			Error: "no_code",
		}
		s.sendResult(result)
		http.Error(w, "No authorization code received", http.StatusBadRequest)
		return
	}

	if state == "" {
		log.Error("No state parameter received in Junie OAuth callback")
		result := &OAuthResult{
			Error: "no_state",
		}
		s.sendResult(result)
		http.Error(w, "No state parameter received", http.StatusBadRequest)
		return
	}

	// Send successful result before redirecting so the main flow can proceed
	result := &OAuthResult{
		Code:  code,
		State: state,
	}
	s.sendResult(result)

	// Redirect browser to the JetBrains post-login completion page
	http.Redirect(w, r, PostLoginURL, http.StatusFound)
}

// sendResult sends the OAuth result to the waiting channel without blocking.
//
// Parameters:
//   - result: The OAuth result to send
func (s *OAuthServer) sendResult(result *OAuthResult) {
	select {
	case s.resultChan <- result:
		log.Debug("Junie OAuth result sent to channel")
	default:
		log.Warn("Junie OAuth result channel is full, result dropped")
	}
}

// IsRunning returns whether the server is currently running.
//
// Returns:
//   - bool: True if the server is running, false otherwise
func (s *OAuthServer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
