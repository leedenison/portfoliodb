package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	authv1 "github.com/leedenison/portfoliodb/proto/auth/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Google OAuth scopes required by the CLI.
var oauthScopes = []string{
	"openid",
	"email",
	"https://www.googleapis.com/auth/spreadsheets",
	"https://www.googleapis.com/auth/drive.file",
}

// credentials is the subset of a Google Cloud OAuth client JSON we need.
type credentials struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURIs []string `json:"redirect_uris"`
}

// credentialsFile wraps the installed app credentials format.
type credentialsFile struct {
	Installed *credentials `json:"installed"`
}

// sessionCache is the on-disk format for the PortfolioDB session.
type sessionCache struct {
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// loadCredentials reads OAuth client credentials from configDir/credentials.json.
// If the file doesn't exist, it creates a template and returns an error.
func loadCredentials(configDir string) (*credentials, error) {
	path := filepath.Join(configDir, "credentials.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, createCredentialsTemplate(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	var cf credentialsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse credentials.json: %w", err)
	}
	if cf.Installed == nil {
		return nil, fmt.Errorf("credentials.json missing 'installed' key; see %s", path)
	}
	if cf.Installed.ClientID == "" || cf.Installed.ClientSecret == "" {
		return nil, fmt.Errorf("credentials.json has empty client_id or client_secret; fill in values from Google Cloud Console")
	}
	return cf.Installed, nil
}

func createCredentialsTemplate(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmpl := credentialsFile{
		Installed: &credentials{
			ClientID:     "<your-client-id>.apps.googleusercontent.com",
			ClientSecret: "<your-client-secret>",
			RedirectURIs: []string{"http://localhost"},
		},
	}
	data, _ := json.MarshalIndent(tmpl, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write template: %w", err)
	}
	return fmt.Errorf("no credentials found; a template has been created at %s -- fill in your Google Cloud OAuth client ID and secret, then re-run", path)
}

// oauthConfig builds an oauth2.Config from the loaded credentials.
func oauthConfig(creds *credentials, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Scopes:       oauthScopes,
		Endpoint:     google.Endpoint,
		RedirectURL:  redirectURL,
	}
}

// googleTokenSource returns an oauth2.TokenSource for the Google Sheets API.
// If no cached token exists, it opens a browser for OAuth. This does NOT
// force a refresh — the library handles refresh transparently for Sheets calls.
func googleTokenSource(ctx context.Context, configDir string) (oauth2.TokenSource, error) {
	creds, err := loadCredentials(configDir)
	if err != nil {
		return nil, err
	}

	tokenPath := filepath.Join(configDir, "token.json")
	tok, err := loadCachedToken(tokenPath)
	cfg := oauthConfig(creds, "http://localhost")

	if err == nil && tok.RefreshToken != "" {
		return cfg.TokenSource(ctx, tok), nil
	}

	ts, _, err := browserAuth(ctx, cfg, tokenPath)
	return ts, err
}

// googleIDToken obtains a fresh Google ID token by forcing a token refresh.
// Called only when the PortfolioDB session has expired and we need to re-auth.
func googleIDToken(ctx context.Context, configDir string) (string, error) {
	creds, err := loadCredentials(configDir)
	if err != nil {
		return "", err
	}

	tokenPath := filepath.Join(configDir, "token.json")
	tok, err := loadCachedToken(tokenPath)
	cfg := oauthConfig(creds, "http://localhost")

	if err == nil && tok.RefreshToken != "" {
		// Force expired so the library refreshes and returns id_token.
		tok.Expiry = time.Now().Add(-time.Minute)
		ts := cfg.TokenSource(ctx, tok)
		fresh, err := ts.Token()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cached token expired, re-authenticating...\n")
			_, idToken, err := browserAuth(ctx, cfg, tokenPath)
			return idToken, err
		}
		idToken, _ := fresh.Extra("id_token").(string)
		if idToken == "" {
			fmt.Fprintf(os.Stderr, "No ID token in refresh response, re-authenticating...\n")
			_, idToken, err := browserAuth(ctx, cfg, tokenPath)
			return idToken, err
		}
		if err := saveCachedToken(tokenPath, fresh); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to save token: %v\n", err)
		}
		return idToken, nil
	}

	_, idToken, err := browserAuth(ctx, cfg, tokenPath)
	return idToken, err
}

// browserAuth runs the installed-app OAuth flow via browser.
func browserAuth(ctx context.Context, cfg *oauth2.Config, tokenPath string) (oauth2.TokenSource, string, error) {
	// Start localhost listener on a random port.
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, "", fmt.Errorf("listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cfg.RedirectURL = fmt.Sprintf("http://localhost:%d/callback", port)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if errMsg := r.URL.Query().Get("error"); errMsg != "" {
			errCh <- fmt.Errorf("oauth error: %s", errMsg)
			fmt.Fprintf(w, "Authentication failed: %s. You can close this tab.", errMsg)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no code in callback")
			fmt.Fprintf(w, "Error: no authorization code received. You can close this tab.")
			return
		}
		codeCh <- code
		fmt.Fprintf(w, "Authentication successful! You can close this tab.")
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Open browser. We request access_type=offline to get a refresh token.
	authURL := cfg.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	fmt.Fprintf(os.Stderr, "Opening browser for Google authentication...\n")
	fmt.Fprintf(os.Stderr, "If the browser does not open, visit:\n%s\n", authURL)
	openBrowser(authURL)

	// Wait for the callback.
	var code string
	select {
	case code = <-codeCh:
	case err := <-errCh:
		return nil, "", err
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, "", fmt.Errorf("token exchange: %w", err)
	}

	if err := saveCachedToken(tokenPath, tok); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to save token: %v\n", err)
	}

	idToken, _ := tok.Extra("id_token").(string)
	return oauth2.StaticTokenSource(tok), idToken, nil
}

func loadCachedToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

func saveCachedToken(path string, tok *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// sessionPath returns ~/.portfoliodb/session (shared across CLI tools).
func sessionPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".portfoliodb", "session"), nil
}

// portfolioDBAuth returns a valid PortfolioDB session ID. Uses a cached
// session from ~/.portfoliodb/session if still valid; otherwise obtains a
// fresh Google ID token and calls AuthUser.
func portfolioDBAuth(ctx context.Context, conn *grpc.ClientConn, configDir string) (string, error) {
	sessPath, err := sessionPath()
	if err != nil {
		return "", err
	}

	// Try cached session.
	if sess, err := loadCachedSession(sessPath); err == nil {
		if time.Now().Before(sess.ExpiresAt.Add(-time.Minute)) {
			return sess.SessionID, nil
		}
	}

	// Session expired or missing — get a fresh ID token.
	idToken, err := googleIDToken(ctx, configDir)
	if err != nil {
		return "", fmt.Errorf("Google authentication: %w", err)
	}
	if idToken == "" {
		return "", fmt.Errorf("no ID token available for PortfolioDB authentication")
	}

	client := authv1.NewAuthServiceClient(conn)
	resp, err := client.AuthUser(ctx, &authv1.AuthUserRequest{GoogleIdToken: idToken})
	if err != nil {
		return "", fmt.Errorf("AuthUser: %w", err)
	}

	sess := &sessionCache{
		SessionID: resp.GetSession().GetSessionId(),
		ExpiresAt: resp.GetSession().GetExpiresAt().AsTime(),
	}
	if err := saveCachedSession(sessPath, sess); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to cache session: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "Authenticated as %s (%s)\n", resp.GetUser().GetEmail(), resp.GetUser().GetRole())
	return sess.SessionID, nil
}

func loadCachedSession(path string) (*sessionCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sess sessionCache
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func saveCachedSession(path string, sess *sessionCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// authContext returns a context with the session ID as Bearer authorization metadata.
func authContext(ctx context.Context, sessionID string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+sessionID)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}
