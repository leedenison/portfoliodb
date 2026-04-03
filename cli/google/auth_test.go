package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestLoadCredentials_MissingFile_CreatesTemplate(t *testing.T) {
	dir := t.TempDir()
	_, err := loadCredentials(dir)
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}

	// Template should exist now.
	path := filepath.Join(dir, "credentials.json")
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("template not created: %v", readErr)
	}
	var cf credentialsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		t.Fatalf("invalid template JSON: %v", err)
	}
	if cf.Installed == nil {
		t.Fatal("template missing installed key")
	}
	if cf.Installed.ClientID == "" {
		t.Fatal("template has empty client_id")
	}
}

func TestLoadCredentials_ValidFile(t *testing.T) {
	dir := t.TempDir()
	cf := credentialsFile{
		Installed: &credentials{
			ClientID:     "test-id.apps.googleusercontent.com",
			ClientSecret: "test-secret",
			RedirectURIs: []string{"http://localhost"},
		},
	}
	data, _ := json.Marshal(cf)
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	creds, err := loadCredentials(dir)
	if err != nil {
		t.Fatalf("loadCredentials: %v", err)
	}
	if creds.ClientID != "test-id.apps.googleusercontent.com" {
		t.Fatalf("expected test-id, got %s", creds.ClientID)
	}
}

func TestLoadCredentials_EmptyClientID(t *testing.T) {
	dir := t.TempDir()
	cf := credentialsFile{
		Installed: &credentials{
			ClientID:     "",
			ClientSecret: "secret",
		},
	}
	data, _ := json.Marshal(cf)
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadCredentials(dir)
	if err == nil {
		t.Fatal("expected error for empty client_id")
	}
}

func TestLoadCredentials_MissingInstalledKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadCredentials(dir)
	if err == nil {
		t.Fatal("expected error for missing installed key")
	}
}

func TestCachedToken_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")

	tok := &oauth2.Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
		Expiry:       time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := saveCachedToken(path, tok); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadCachedToken(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.AccessToken != "access-123" {
		t.Fatalf("expected access-123, got %s", loaded.AccessToken)
	}
	if loaded.RefreshToken != "refresh-456" {
		t.Fatalf("expected refresh-456, got %s", loaded.RefreshToken)
	}
}

func TestCachedToken_MissingFile(t *testing.T) {
	_, err := loadCachedToken(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCachedSession_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	sess := &sessionCache{
		SessionID: "sess-abc",
		ExpiresAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := saveCachedSession(path, sess); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadCachedSession(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.SessionID != "sess-abc" {
		t.Fatalf("expected sess-abc, got %s", loaded.SessionID)
	}
	if !loaded.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Fatalf("expected %v, got %v", sess.ExpiresAt, loaded.ExpiresAt)
	}
}

func TestCachedSession_MissingFile(t *testing.T) {
	_, err := loadCachedSession(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCreateCredentialsTemplate_PermissionsRestrictive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	_ = createCredentialsTemplate(path) // always returns error

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Fatalf("expected 0600 permissions, got %o", perm)
	}
}
