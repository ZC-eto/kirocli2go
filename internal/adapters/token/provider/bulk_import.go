package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type BulkImportRequest struct {
	Content       string `json:"content"`
	DefaultWeight int    `json:"default_weight,omitempty"`
	ProxyGroupID  string `json:"proxy_group_id,omitempty"`
}

type BulkImportResult struct {
	Imported int               `json:"imported"`
	Failed   int               `json:"failed"`
	Accounts []AccountSnapshot `json:"accounts,omitempty"`
	Errors   []string          `json:"errors,omitempty"`
}

type bulkImportRecord struct {
	ID              string `json:"id"`
	Weight          int    `json:"weight"`
	BearerToken     string `json:"bearer_token"`
	BearerTokenAlt  string `json:"bearerToken"`
	RefreshToken    string `json:"refresh_token"`
	RefreshTokenAlt string `json:"refreshToken"`
	ClientID        string `json:"client_id"`
	ClientIDAlt     string `json:"clientId"`
	ClientSecret    string `json:"client_secret"`
	ClientSecretAlt string `json:"clientSecret"`
	ProxyGroupID    string `json:"proxy_group_id"`
	ProxyGroupIDAlt string `json:"proxyGroupId"`
}

func (p *Provider) ImportAccountsBulk(ctx context.Context, req BulkImportRequest) (BulkImportResult, error) {
	_ = ctx

	records, err := parseBulkImportRecords(req.Content)
	if err != nil {
		return BulkImportResult{}, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	result := BulkImportResult{
		Accounts: make([]AccountSnapshot, 0, len(records)),
		Errors:   make([]string, 0),
	}

	defaultProxyGroupID := strings.TrimSpace(req.ProxyGroupID)
	if defaultProxyGroupID != "" {
		if _, err := p.proxyGroupLocked(defaultProxyGroupID); err != nil {
			return BulkImportResult{}, err
		}
	}

	for idx, record := range records {
		itemReq := record.toImportRequest(req.DefaultWeight, defaultProxyGroupID)
		accountSnapshot, err := p.importAccountLocked(itemReq)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, fmt.Sprintf("item %d: %v", idx+1, err))
			continue
		}
		result.Imported++
		result.Accounts = append(result.Accounts, accountSnapshot)
	}

	if result.Imported == 0 && len(result.Errors) > 0 {
		return result, fmt.Errorf("no accounts imported")
	}
	if result.Imported > 0 {
		if err := p.saveStateLocked(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func parseBulkImportRecords(content string) ([]bulkImportRecord, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("content is required")
	}

	var records []bulkImportRecord
	if err := json.Unmarshal([]byte(trimmed), &records); err != nil {
		return nil, fmt.Errorf("invalid account batch json: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("account batch is empty")
	}
	return records, nil
}

func (r bulkImportRecord) toImportRequest(defaultWeight int, defaultProxyGroupID string) ImportRequest {
	req := ImportRequest{
		ID:           strings.TrimSpace(r.ID),
		Weight:       r.Weight,
		BearerToken:  firstNonEmpty(r.BearerToken, r.BearerTokenAlt),
		RefreshToken: firstNonEmpty(r.RefreshToken, r.RefreshTokenAlt),
		ClientID:     firstNonEmpty(r.ClientID, r.ClientIDAlt),
		ClientSecret: firstNonEmpty(r.ClientSecret, r.ClientSecretAlt),
		ProxyGroupID: firstNonEmpty(r.ProxyGroupID, r.ProxyGroupIDAlt, defaultProxyGroupID),
	}
	if req.Weight <= 0 {
		req.Weight = defaultWeight
	}
	return req
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
