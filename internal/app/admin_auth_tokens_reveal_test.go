package app

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"ccLoad/internal/model"
	"ccLoad/internal/site/credential"

	"github.com/gin-gonic/gin"
)

func TestAuthTokenRevealLifecycle(t *testing.T) {
	server := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "auth-token-reveal-test")
	if err != nil {
		t.Fatal(err)
	}
	server.siteControl.cipher = cipher

	createContext, createRecorder := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/auth-tokens", map[string]any{
		"description": "recoverable token",
	}))
	server.HandleCreateAuthToken(createContext)
	if createRecorder.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	type tokenData struct {
		ID               int64  `json:"id"`
		Token            string `json:"token"`
		TokenHint        string `json:"token_hint"`
		TokenRecoverable bool   `json:"token_recoverable"`
	}
	created := mustParseAPIResponse[tokenData](t, createRecorder.Body.Bytes())
	if !created.Success || created.Data.ID == 0 || created.Data.Token == "" || created.Data.TokenHint == "" || !created.Data.TokenRecoverable {
		t.Fatalf("unexpected create response: %+v", created)
	}
	plain := created.Data.Token
	hash := model.HashToken(plain)
	stored, err := server.store.GetAuthToken(context.Background(), created.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Token != hash || stored.TokenCiphertext == "" {
		t.Fatalf("stored token is not hashed and encrypted: %+v", stored)
	}

	listContext, listRecorder := newTestContext(t, newRequest(http.MethodGet, "/admin/auth-tokens", nil))
	server.HandleListAuthTokens(listContext)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	listBody := listRecorder.Body.String()
	if strings.Contains(listBody, plain) || strings.Contains(listBody, hash) {
		t.Fatalf("token secret or hash leaked from list response")
	}
	if !strings.Contains(listBody, `"token_recoverable":true`) {
		t.Fatalf("recoverable flag missing: %s", listBody)
	}

	revealContext, revealRecorder := newTestContext(t, newRequest(http.MethodGet, "/admin/auth-tokens/1/reveal", nil))
	revealContext.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(created.Data.ID, 10)}}
	server.HandleRevealAuthToken(revealContext)
	if revealRecorder.Code != http.StatusOK {
		t.Fatalf("reveal status=%d body=%s", revealRecorder.Code, revealRecorder.Body.String())
	}
	revealed := mustParseAPIResponse[tokenData](t, revealRecorder.Body.Bytes())
	if revealed.Data.Token != plain {
		t.Fatal("revealed token does not match the created token")
	}
}

func TestRevealLegacyAuthTokenReturnsConflict(t *testing.T) {
	server := newInMemoryServer(t)
	legacy := &model.AuthToken{Token: model.HashToken("legacy-token"), Description: "legacy", IsActive: true}
	if err := server.store.CreateAuthToken(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	c, recorder := newTestContext(t, newRequest(http.MethodGet, "/admin/auth-tokens/1/reveal", nil))
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(legacy.ID, 10)}}
	server.HandleRevealAuthToken(c)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestCreateAuthTokenRejectsUnavailableMasterKey(t *testing.T) {
	server := newInMemoryServer(t)
	server.siteControl.cipher = nil
	c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/auth-tokens", map[string]any{
		"description": "cannot recover",
	}))
	server.HandleCreateAuthToken(c)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
}
