package sql_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestSystemAccessTokenLifecycle(t *testing.T) {
	t.Parallel()
	store := newTestStore(t, "system_access_tokens.db")
	ctx := context.Background()
	plain := "pf_sys_test_secret"
	token := &model.SystemAccessToken{
		Token: model.HashSystemAccessToken(plain), TokenHint: model.MaskSystemAccessToken(plain),
		Description: "diagnostic bot", Scopes: []string{model.SystemAccessScopeChannelsRead},
		CreatedAt: time.Now().UnixMilli(), IsActive: true,
	}
	if err := store.CreateSystemAccessToken(ctx, token); err != nil {
		t.Fatalf("create token: %v", err)
	}
	got, err := store.GetSystemAccessTokenByHash(ctx, model.HashSystemAccessToken(plain))
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if got.ID != token.ID || got.Description != token.Description || got.Token != model.HashSystemAccessToken(plain) {
		t.Fatalf("unexpected token: %+v", got)
	}
	if got.Token == plain {
		t.Fatal("storage must not retain the plaintext token")
	}
	got.Scopes = []string{model.SystemAccessScopeLogsRead}
	got.IsActive = false
	if err := store.UpdateSystemAccessToken(ctx, got); err != nil {
		t.Fatalf("update token: %v", err)
	}
	if err := store.UpdateSystemAccessTokenLastUsed(ctx, got.Token, time.Now()); err != nil {
		t.Fatalf("update last used: %v", err)
	}
	listed, err := store.ListSystemAccessTokens(ctx)
	if err != nil || len(listed) != 1 || listed[0].LastUsedAt == nil || listed[0].IsActive {
		t.Fatalf("list token: %+v, err=%v", listed, err)
	}
	if err := store.DeleteSystemAccessToken(ctx, got.ID); err != nil {
		t.Fatalf("delete token: %v", err)
	}
	if _, err := store.GetSystemAccessTokenByHash(ctx, got.Token); !errors.Is(err, model.ErrSystemAccessTokenNotFound) {
		t.Fatalf("get deleted error=%v", err)
	}
}

func TestSystemAccessTokenRejectsUnknownScope(t *testing.T) {
	t.Parallel()
	store := newTestStore(t, "system_access_scope.db")
	err := store.CreateSystemAccessToken(context.Background(), &model.SystemAccessToken{Token: "hash", Description: "bad", Scopes: []string{"admin.write"}, IsActive: true})
	if err == nil {
		t.Fatal("expected unknown scope to be rejected")
	}
}
