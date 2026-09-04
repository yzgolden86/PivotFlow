package provider

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// Veloera keeps the New API resource layout but uses its own check-in endpoint
// and compatibility user header.
type Veloera struct{ family *NewAPI }

func NewVeloera(factory ClientFactory) *Veloera { return &Veloera{family: NewNewAPI(factory)} }
func (p *Veloera) ID() string                   { return model.SitePlatformVeloera }
func (p *Veloera) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		ServerCheckin: true, Balance: true, Models: true, Announcements: true,
		CredentialTypes: []string{model.CredentialTypeUsernamePassword, model.CredentialTypeAccessToken, model.CredentialTypeCookie, model.CredentialTypeAPIKey},
	}
}

func (p *Veloera) Detect(ctx context.Context, baseURL string) (DetectionResult, error) {
	var payload envelope
	if err := p.family.doJSON(ctx, AccountRequest{BaseURL: baseURL}, http.MethodGet, "/api/status", nil, &payload); err != nil {
		return DetectionResult{}, err
	}
	name, _ := stringValue(payload.Data, "system_name")
	version, _ := stringValue(payload.Data, "version")
	matched := payload.Success && strings.Contains(strings.ToLower(name+" "+version), "veloera")
	return DetectionResult{Matched: matched, ProviderID: p.ID(), SystemName: name, Capabilities: p.Capabilities()}, nil
}

func (p *Veloera) RefreshAccount(ctx context.Context, req RefreshAccountRequest) (AccountSnapshot, error) {
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		return AccountSnapshot{}, &Error{Code: CodeUnsupported, Message: "Veloera balance refresh requires a session token"}
	}
	var payload envelope
	if err := p.family.doJSONWithHeaders(ctx, req, http.MethodGet, "/api/user/self", nil, veloeraHeaders(req.Credentials), &payload); err != nil {
		return AccountSnapshot{}, err
	}
	if !payload.Success {
		return AccountSnapshot{}, responseError(payload, http.StatusOK)
	}
	username, _ := stringValue(payload.Data, "username")
	quota, quotaOK := numberValue(payload.Data, "quota")
	used, usedOK := numberValue(payload.Data, "used_quota")
	var balance *float64
	if quotaOK {
		remaining := quota
		if usedOK {
			remaining -= used
		}
		if remaining < 0 {
			remaining = 0
		}
		value := remaining / 1000000
		balance = &value
	}
	return AccountSnapshot{Username: username, Balance: balance, Currency: "USD", Status: model.SiteAccountStatusHealthy}, nil
}

func (p *Veloera) Login(ctx context.Context, req LoginRequest) (Credentials, error) {
	return p.family.Login(ctx, req)
}

func (p *Veloera) ResolveManagementCredentials(ctx context.Context, req AccountRequest) (Credentials, error) {
	return p.family.ResolveManagementCredentials(ctx, req)
}

func (p *Veloera) ListRoutingKeys(ctx context.Context, req AccountRequest) ([]RoutingKeySnapshot, error) {
	return p.family.ListRoutingKeys(ctx, req)
}

func (p *Veloera) ListModels(ctx context.Context, req AccountRequest) ([]ModelSnapshot, error) {
	return p.family.ListModels(ctx, req)
}

// FetchPricing reads the site's own price table. Veloera keeps the New API
// /api/pricing layout, and applyAuth already sends the Veloera-User header
// variant alongside the family ones, so the family implementation applies
// unchanged.
func (p *Veloera) FetchPricing(ctx context.Context, req AccountRequest) (SitePricing, error) {
	return p.family.FetchPricing(ctx, req)
}

func (p *Veloera) Checkin(ctx context.Context, req AccountRequest) (CheckinResult, error) {
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		return CheckinResult{Status: CheckinUnsupported}, &Error{Code: CodeUnsupported, Message: "Veloera check-in requires a session token"}
	}
	var payload envelope
	err := p.family.doJSONWithHeaders(ctx, req, http.MethodPost, "/api/user/check_in", nil, veloeraHeaders(req.Credentials), &payload)
	if err != nil {
		return checkinErrorResult(err)
	}
	message := strings.TrimSpace(payload.Message)
	if isAlreadyCheckedMessage(message) {
		return CheckinResult{Status: CheckinAlreadyChecked, Message: message}, nil
	}
	if payload.Success {
		reward, _ := stringValue(payload.Data, "reward")
		return CheckinResult{Status: CheckinSuccess, RewardText: reward, Message: message}, nil
	}
	return CheckinResult{Status: CheckinFailed, Message: message}, responseError(payload, http.StatusOK)
}

func (p *Veloera) ListAnnouncements(ctx context.Context, req AccountRequest) ([]Announcement, error) {
	return p.family.ListAnnouncements(ctx, req)
}

func veloeraHeaders(credentials Credentials) map[string]string {
	if credentials.UserID <= 0 {
		return nil
	}
	value := strconv.FormatInt(credentials.UserID, 10)
	return map[string]string{"Veloera-User": value, "New-API-User": value, "User-id": value}
}
