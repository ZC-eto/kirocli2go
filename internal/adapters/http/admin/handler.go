package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	runtimecatalog "kirocli-go/internal/adapters/catalog/runtime"
	"kirocli-go/internal/adapters/token/provider"
	appstats "kirocli-go/internal/application/stats"
	"kirocli-go/internal/config"
	"kirocli-go/internal/domain/model"
)

type Handler struct {
	stats       *appstats.Collector
	requestLogs *appstats.RequestLogRing
	cfg         config.Config
	provider    interface {
		SnapshotAccounts() []provider.AccountSnapshot
		PoolSnapshot() provider.PoolSnapshot
		WarmPool(ctx context.Context) (provider.PoolSnapshot, error)
		RefreshPool(ctx context.Context) (provider.PoolSnapshot, error)
		DisableAccount(id string) error
		EnableAccount(id string) error
		DeleteAccount(id string) error
		UpdateWeight(id string, weight int) error
		RefreshAccount(ctx context.Context, id string) (provider.AccountSnapshot, error)
		ImportAccount(ctx context.Context, req provider.ImportRequest) (provider.AccountSnapshot, error)
		ImportAccountsBulk(ctx context.Context, req provider.BulkImportRequest) (provider.BulkImportResult, error)
		ExportAccounts() []provider.ManagedExport
		ListProxyGroups() []provider.ProxyGroupSnapshot
		ImportProxyGroups(ctx context.Context, req provider.ProxyGroupImportRequest) (provider.ProxyGroupImportResult, error)
		EnableProxyGroup(id string) error
		DisableProxyGroup(id string) error
		DeleteProxyGroup(id string) error
		AssignProxyGroups(ctx context.Context, req provider.ProxyGroupAssignRequest) (provider.ProxyGroupAssignResult, error)
	}
	catalog interface {
		List(ctx context.Context) ([]model.ResolvedModel, error)
		Refresh(ctx context.Context) (int, error)
		Snapshot() runtimecatalog.Snapshot
	}
}

type bulkImportAccountRequest struct {
	ID           string `json:"id,omitempty"`
	Weight       int    `json:"weight,omitempty"`
	BearerToken  string `json:"bearer_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	ProxyGroupID string `json:"proxy_group_id,omitempty"`
}

type bulkImportRequest struct {
	Accounts []bulkImportAccountRequest `json:"accounts,omitempty"`
}

type bulkImportError struct {
	Index int    `json:"index"`
	ID    string `json:"id,omitempty"`
	Error string `json:"error"`
}

type proxyGroupSnapshot struct {
	ID                string `json:"id"`
	Name              string `json:"name,omitempty"`
	ProxyURLMasked    string `json:"proxy_url_masked,omitempty"`
	Enabled           bool   `json:"enabled"`
	Notes             string `json:"notes,omitempty"`
	BoundAccountCount int    `json:"bound_account_count,omitempty"`
	CreatedAt         int64  `json:"created_at,omitempty"`
	UpdatedAt         int64  `json:"updated_at,omitempty"`
}

type proxyGroupImportItem struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	ProxyURL string `json:"proxy_url,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

type proxyGroupImportRequest struct {
	Groups []proxyGroupImportItem `json:"groups,omitempty"`
}

type proxyGroupAssignment struct {
	AccountID    string `json:"account_id,omitempty"`
	ProxyGroupID string `json:"proxy_group_id,omitempty"`
}

type proxyGroupAssignRequest struct {
	AccountIDs       []string               `json:"account_ids,omitempty"`
	ProxyGroupIDs    []string               `json:"proxy_group_ids,omitempty"`
	AccountsPerProxy int                    `json:"accounts_per_proxy,omitempty"`
	Assignments      []proxyGroupAssignment `json:"assignments,omitempty"`
}

func NewHandler(
	cfg config.Config,
	statsCollector *appstats.Collector,
	requestLogs *appstats.RequestLogRing,
	accountProvider interface {
		SnapshotAccounts() []provider.AccountSnapshot
		PoolSnapshot() provider.PoolSnapshot
		WarmPool(ctx context.Context) (provider.PoolSnapshot, error)
		RefreshPool(ctx context.Context) (provider.PoolSnapshot, error)
		DisableAccount(id string) error
		EnableAccount(id string) error
		DeleteAccount(id string) error
		UpdateWeight(id string, weight int) error
		RefreshAccount(ctx context.Context, id string) (provider.AccountSnapshot, error)
		ImportAccount(ctx context.Context, req provider.ImportRequest) (provider.AccountSnapshot, error)
		ImportAccountsBulk(ctx context.Context, req provider.BulkImportRequest) (provider.BulkImportResult, error)
		ExportAccounts() []provider.ManagedExport
		ListProxyGroups() []provider.ProxyGroupSnapshot
		ImportProxyGroups(ctx context.Context, req provider.ProxyGroupImportRequest) (provider.ProxyGroupImportResult, error)
		EnableProxyGroup(id string) error
		DisableProxyGroup(id string) error
		DeleteProxyGroup(id string) error
		AssignProxyGroups(ctx context.Context, req provider.ProxyGroupAssignRequest) (provider.ProxyGroupAssignResult, error)
	},
	modelCatalog interface {
		List(ctx context.Context) ([]model.ResolvedModel, error)
		Refresh(ctx context.Context) (int, error)
		Snapshot() runtimecatalog.Snapshot
	},
) *Handler {
	return &Handler{
		cfg:         cfg,
		stats:       statsCollector,
		requestLogs: requestLogs,
		provider:    accountProvider,
		catalog:     modelCatalog,
	}
}

type bulkAccountImporter interface {
	ImportAccountsBulk(ctx context.Context, req provider.BulkImportRequest) (provider.BulkImportResult, error)
}

type proxyGroupLister interface {
	ListProxyGroups() []provider.ProxyGroupSnapshot
}

type proxyGroupImporter interface {
	ImportProxyGroups(ctx context.Context, req provider.ProxyGroupImportRequest) (provider.ProxyGroupImportResult, error)
}

type proxyGroupMutator interface {
	EnableProxyGroup(id string) error
	DisableProxyGroup(id string) error
	DeleteProxyGroup(id string) error
	AssignProxyGroups(ctx context.Context, req provider.ProxyGroupAssignRequest) (provider.ProxyGroupAssignResult, error)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/api")
	w.Header().Set("Content-Type", "application/json")

	switch {
	case path == "/version" && r.Method == http.MethodGet:
		h.handleVersion(w)
	case path == "/config" && r.Method == http.MethodGet:
		h.handleConfig(w)
	case path == "/doctor" && r.Method == http.MethodGet:
		h.handleDoctor(w)
	case path == "/accounts" && r.Method == http.MethodGet:
		h.handleAccounts(w)
	case path == "/accounts/import" && r.Method == http.MethodPost:
		h.handleImport(w, r)
	case path == "/accounts/import/bulk" && r.Method == http.MethodPost:
		h.handleImportBulk(w, r)
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/disable") && r.Method == http.MethodPost:
		h.handleDisable(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/disable"))
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/enable") && r.Method == http.MethodPost:
		h.handleEnable(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/enable"))
	case strings.HasPrefix(path, "/accounts/") && r.Method == http.MethodDelete:
		h.handleDelete(w, strings.TrimPrefix(path, "/accounts/"))
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/refresh") && r.Method == http.MethodPost:
		h.handleRefresh(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/refresh"))
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/weight") && r.Method == http.MethodPost:
		h.handleWeight(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/weight"))
	case path == "/proxy-groups" && r.Method == http.MethodGet:
		h.handleProxyGroups(w)
	case path == "/proxy-groups/import" && r.Method == http.MethodPost:
		h.handleProxyGroupImport(w, r)
	case strings.HasPrefix(path, "/proxy-groups/") && strings.HasSuffix(path, "/enable") && r.Method == http.MethodPost:
		h.handleProxyGroupEnable(w, strings.TrimSuffix(strings.TrimPrefix(path, "/proxy-groups/"), "/enable"))
	case strings.HasPrefix(path, "/proxy-groups/") && strings.HasSuffix(path, "/disable") && r.Method == http.MethodPost:
		h.handleProxyGroupDisable(w, strings.TrimSuffix(strings.TrimPrefix(path, "/proxy-groups/"), "/disable"))
	case strings.HasPrefix(path, "/proxy-groups/") && r.Method == http.MethodDelete:
		h.handleProxyGroupDelete(w, strings.TrimPrefix(path, "/proxy-groups/"))
	case path == "/proxy-groups/assign" && r.Method == http.MethodPost:
		h.handleProxyGroupAssign(w, r)
	case path == "/export" && r.Method == http.MethodGet:
		h.handleExport(w)
	case path == "/models" && r.Method == http.MethodGet:
		h.handleModels(w, r)
	case path == "/models/refresh" && r.Method == http.MethodPost:
		h.handleRefreshModels(w, r)
	case path == "/pool/warm" && r.Method == http.MethodPost:
		h.handleWarmPool(w, r)
	case path == "/pool/refresh" && r.Method == http.MethodPost:
		h.handleRefreshPool(w, r)
	case path == "/request-logs" && r.Method == http.MethodGet:
		h.handleRequestLogs(w, r)
	case path == "/status" && r.Method == http.MethodGet:
		h.handleStatus(w)
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not found"})
	}
}

func (h *Handler) handleVersion(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": config.Version,
	})
}

func (h *Handler) handleConfig(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"server": map[string]any{
			"address": h.cfg.Server.Address,
		},
		"accounts": map[string]any{
			"source":           h.cfg.Accounts.Source,
			"csv_path":         h.cfg.Accounts.CSVPath,
			"api_url":          h.cfg.Accounts.APIURL,
			"api_category_id":  h.cfg.Accounts.APICategoryID,
			"active_pool_size": h.cfg.Accounts.ActivePoolSize,
			"max_refresh_try":  h.cfg.Accounts.MaxRefreshTry,
			"state_path":       h.cfg.Accounts.StatePath,
			"proxy_state_path": h.cfg.Accounts.ProxyStatePath,
			"oidc_url":         h.cfg.Accounts.OIDCURL,
		},
		"models": map[string]any{
			"thinking_suffix": h.cfg.Models.ThinkingSuffix,
		},
		"upstream": map[string]any{
			"cli_base_url":   h.cfg.Upstream.CLIBaseURL,
			"cli_models_url": h.cfg.Upstream.CLIModelsURL,
			"cli_origin":     h.cfg.Upstream.CLIOrigin,
		},
	})
}

func (h *Handler) handleDoctor(w http.ResponseWriter) {
	checks := []map[string]any{
		pathCheck("account_state", h.cfg.Accounts.StatePath),
		pathCheck("proxy_state", h.cfg.Accounts.ProxyStatePath),
		pathCheck("stats_state", h.cfg.State.StatsPath),
		pathCheck("catalog_state", h.cfg.State.CatalogPath),
	}

	accountConfigured := false
	switch strings.ToLower(strings.TrimSpace(h.cfg.Accounts.Source)) {
	case "env":
		accountConfigured = strings.TrimSpace(h.cfg.Accounts.BearerToken) != ""
	case "csv":
		accountConfigured = strings.TrimSpace(h.cfg.Accounts.CSVPath) != ""
	case "api":
		accountConfigured = strings.TrimSpace(h.cfg.Accounts.APIURL) != "" && strings.TrimSpace(h.cfg.Accounts.APIToken) != ""
	case "auto", "":
		accountConfigured = strings.TrimSpace(h.cfg.Accounts.BearerToken) != "" ||
			strings.TrimSpace(h.cfg.Accounts.CSVPath) != "" ||
			(strings.TrimSpace(h.cfg.Accounts.APIURL) != "" && strings.TrimSpace(h.cfg.Accounts.APIToken) != "")
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"checks": checks,
		"runtime": map[string]any{
			"version":               config.Version,
			"account_source":        h.cfg.Accounts.Source,
			"account_configured":    accountConfigured,
			"model_refresh_enabled": h.cfg.Background.ModelRefreshEnabled,
			"state_persist_enabled": h.cfg.State.PersistEnabled,
		},
	})
}

func pathCheck(name, path string) map[string]any {
	info := map[string]any{
		"name": name,
		"path": path,
	}
	if strings.TrimSpace(path) == "" {
		info["ok"] = false
		info["message"] = "path not configured"
		return info
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		info["ok"] = false
		info["message"] = "parent directory missing"
		return info
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			info["ok"] = true
			info["message"] = "state file not created yet"
			return info
		}
		info["ok"] = false
		info["message"] = err.Error()
		return info
	}

	info["ok"] = true
	info["message"] = "ready"
	return info
}

func (h *Handler) handleAccounts(w http.ResponseWriter) {
	accounts := []provider.AccountSnapshot{}
	if h.provider != nil {
		accounts = h.provider.SnapshotAccounts()
	}

	views := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		views = append(views, accountSnapshotView(account))
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"accounts": views,
	})
}

func (h *Handler) handleImportBulk(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var payload struct {
		Content       string `json:"content"`
		DefaultWeight int    `json:"default_weight,omitempty"`
		ProxyGroupID  string `json:"proxy_group_id,omitempty"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		payload = struct {
			Content       string `json:"content"`
			DefaultWeight int    `json:"default_weight,omitempty"`
			ProxyGroupID  string `json:"proxy_group_id,omitempty"`
		}{}
	}

	content := strings.TrimSpace(payload.Content)
	if content == "" {
		items, parseErr := parseBulkImportAccounts(bytes.NewReader(body))
		if parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
		if len(items) == 0 {
			http.Error(w, "accounts is required", http.StatusBadRequest)
			return
		}
		contentBytes, marshalErr := json.Marshal(items)
		if marshalErr != nil {
			http.Error(w, marshalErr.Error(), http.StatusBadRequest)
			return
		}
		content = string(contentBytes)
	}

	result, err := h.provider.ImportAccountsBulk(r.Context(), provider.BulkImportRequest{
		Content:       content,
		DefaultWeight: payload.DefaultWeight,
		ProxyGroupID:  strings.TrimSpace(payload.ProxyGroupID),
	})
	if err != nil {
		status := http.StatusBadRequest
		if result.Imported > 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	}
	views := make([]map[string]any, 0, len(result.Accounts))
	for _, snapshot := range result.Accounts {
		views = append(views, accountSnapshotView(snapshot))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":  err == nil && result.Failed == 0 && len(result.Errors) == 0,
		"partial":  result.Imported > 0 && (result.Failed > 0 || len(result.Errors) > 0),
		"message":  batchImportMessage("accounts", result.Imported, result.Failed, result.Errors, err),
		"imported": result.Imported,
		"failed":   result.Failed,
		"errors":   result.Errors,
		"accounts": views,
	})
}

func (h *Handler) handleProxyGroups(w http.ResponseWriter) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	groups := h.provider.ListProxyGroups()
	sort.SliceStable(groups, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(groups[i].Name))
		right := strings.ToLower(strings.TrimSpace(groups[j].Name))
		if left == right {
			return strings.ToLower(strings.TrimSpace(groups[i].ID)) < strings.ToLower(strings.TrimSpace(groups[j].ID))
		}
		return left < right
	})

	views := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		views = append(views, proxyGroupSnapshotView(group))
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"available": true,
		"groups":    views,
	})
}

func (h *Handler) handleProxyGroupImport(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var payload struct {
		Content    string                 `json:"content"`
		NamePrefix string                 `json:"name_prefix,omitempty"`
		Groups     []proxyGroupImportItem `json:"groups,omitempty"`
		Items      []proxyGroupImportItem `json:"items,omitempty"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		payload = struct {
			Content    string                 `json:"content"`
			NamePrefix string                 `json:"name_prefix,omitempty"`
			Groups     []proxyGroupImportItem `json:"groups,omitempty"`
			Items      []proxyGroupImportItem `json:"items,omitempty"`
		}{}
	}

	content := strings.TrimSpace(payload.Content)
	groups := convertProxyGroupImportItems(payload.Groups)
	if len(groups) == 0 {
		groups = convertProxyGroupImportItems(payload.Items)
	}
	if content == "" {
		items, parseErr := parseProxyGroupImportItems(bytes.NewReader(body))
		if parseErr == nil {
			groups = convertProxyGroupImportItems(items)
		}
		if len(groups) == 0 {
			rawContent := strings.TrimSpace(string(body))
			if rawContent != "" {
				content = rawContent
			}
		}
	}
	if content == "" && len(groups) == 0 {
		http.Error(w, "groups or content is required", http.StatusBadRequest)
		return
	}

	result, err := h.provider.ImportProxyGroups(r.Context(), provider.ProxyGroupImportRequest{
		Content:    content,
		NamePrefix: strings.TrimSpace(payload.NamePrefix),
		Groups:     groups,
	})
	if err != nil {
		status := http.StatusBadRequest
		if result.Imported > 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": err == nil && len(result.Errors) == 0,
		"partial": result.Imported > 0 && len(result.Errors) > 0,
		"message": batchImportMessage("proxy groups", result.Imported, 0, result.Errors, err),
		"result":  result,
	})
}

func (h *Handler) handleProxyGroupEnable(w http.ResponseWriter, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.EnableProxyGroup(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleProxyGroupDisable(w http.ResponseWriter, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.DisableProxyGroup(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleProxyGroupDelete(w http.ResponseWriter, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.DeleteProxyGroup(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleProxyGroupAssign(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	var req provider.ProxyGroupAssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	result, err := h.provider.AssignProxyGroups(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
}

func accountSnapshotView(snapshot provider.AccountSnapshot) map[string]any {
	payload := map[string]any{}
	raw, err := json.Marshal(snapshot)
	if err == nil {
		_ = json.Unmarshal(raw, &payload)
	}

	if _, ok := payload["proxy_group_id"]; !ok {
		payload["proxy_group_id"] = ""
	}
	if _, ok := payload["proxy_group_name"]; !ok {
		payload["proxy_group_name"] = ""
	}
	if _, ok := payload["proxy_url_masked"]; !ok {
		payload["proxy_url_masked"] = ""
	}
	if _, ok := payload["proxy_url"]; !ok {
		payload["proxy_url"] = payload["proxy_url_masked"]
	}
	return payload
}

func proxyGroupSnapshotView(snapshot provider.ProxyGroupSnapshot) map[string]any {
	return map[string]any{
		"id":                  snapshot.ID,
		"name":                snapshot.Name,
		"proxy_url":           snapshot.ProxyURLMasked,
		"proxy_url_masked":    snapshot.ProxyURLMasked,
		"enabled":             snapshot.Enabled,
		"notes":               snapshot.Notes,
		"account_count":       snapshot.BoundAccountCount,
		"bound_account_count": snapshot.BoundAccountCount,
		"created_at":          snapshot.CreatedAt,
		"updated_at":          snapshot.UpdatedAt,
	}
}

func convertProxyGroupImportItems(items []proxyGroupImportItem) []provider.ProxyGroupImportItem {
	result := make([]provider.ProxyGroupImportItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ProxyURL) == "" {
			continue
		}
		result = append(result, provider.ProxyGroupImportItem{
			ID:       strings.TrimSpace(item.ID),
			Name:     strings.TrimSpace(item.Name),
			ProxyURL: strings.TrimSpace(item.ProxyURL),
			Enabled:  item.Enabled,
			Notes:    strings.TrimSpace(item.Notes),
		})
	}
	return result
}

func batchImportMessage(kind string, imported, failed int, errors []string, opErr error) string {
	if opErr == nil && failed == 0 && len(errors) == 0 {
		return fmt.Sprintf("%s imported: %d", kind, imported)
	}
	if imported > 0 {
		return fmt.Sprintf("%s imported: %d, failed: %d", kind, imported, failed)
	}
	if opErr != nil {
		return opErr.Error()
	}
	if len(errors) > 0 {
		return strings.Join(errors, "; ")
	}
	return fmt.Sprintf("%s import failed", kind)
}

func parseBulkImportAccounts(r io.Reader) ([]bulkImportAccountRequest, error) {
	items, err := decodeAnyAccounts(r)
	if err != nil {
		return nil, err
	}

	normalized := make([]bulkImportAccountRequest, 0, len(items))
	for _, item := range items {
		normalized = append(normalized, normalizeBulkAccount(item))
	}
	return normalized, nil
}

func decodeAnyAccounts(r io.Reader) ([]map[string]any, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("empty request body")
	}

	if items, ok, err := decodeAccountsArray(body); ok {
		return items, err
	}

	var wrapper struct {
		Accounts []map[string]any `json:"accounts"`
		Items    []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, err
	}
	if len(wrapper.Accounts) > 0 {
		return wrapper.Accounts, nil
	}
	if len(wrapper.Items) > 0 {
		return wrapper.Items, nil
	}
	return nil, fmt.Errorf("accounts is required")
}

func decodeAccountsArray(body []byte) ([]map[string]any, bool, error) {
	var array []map[string]any
	if err := json.Unmarshal(body, &array); err == nil {
		if len(array) == 0 {
			return nil, true, fmt.Errorf("accounts is required")
		}
		return array, true, nil
	}
	return nil, false, nil
}

func normalizeBulkAccount(item map[string]any) bulkImportAccountRequest {
	return bulkImportAccountRequest{
		ID:           firstString(item, "id", "email", "clientId", "client_id"),
		Weight:       firstInt(item, 100, "weight"),
		BearerToken:  firstString(item, "bearer_token", "bearerToken"),
		RefreshToken: firstString(item, "refresh_token", "refreshToken"),
		ClientID:     firstString(item, "client_id", "clientId"),
		ClientSecret: firstString(item, "client_secret", "clientSecret"),
		ProxyGroupID: firstString(item, "proxy_group_id", "proxyGroupId"),
	}
}

func parseProxyGroupImportItems(r io.Reader) ([]proxyGroupImportItem, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("empty request body")
	}

	var array []map[string]any
	if err := json.Unmarshal(body, &array); err == nil && len(array) > 0 {
		return normalizeProxyGroupItems(array), nil
	}

	var wrapper struct {
		Groups []map[string]any `json:"groups"`
		Items  []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		lines := bytes.Split(body, []byte{'\n'})
		result := make([]proxyGroupImportItem, 0, len(lines))
		for idx, rawLine := range lines {
			line := strings.TrimSpace(string(rawLine))
			if line == "" {
				continue
			}
			result = append(result, normalizeProxyGroupImportEntry(line, idx))
		}
		if len(result) == 0 {
			return nil, err
		}
		return result, nil
	}
	if len(wrapper.Groups) > 0 {
		return normalizeProxyGroupItems(wrapper.Groups), nil
	}
	if len(wrapper.Items) > 0 {
		return normalizeProxyGroupItems(wrapper.Items), nil
	}
	return nil, fmt.Errorf("groups is required")
}

func normalizeProxyGroupItems(items []map[string]any) []proxyGroupImportItem {
	result := make([]proxyGroupImportItem, 0, len(items))
	for idx, item := range items {
		name := firstString(item, "name", "title")
		if name == "" {
			name = fmt.Sprintf("proxy-group-%d", idx+1)
		}
		result = append(result, proxyGroupImportItem{
			ID:       firstString(item, "id", "proxy_group_id", "proxyGroupId"),
			Name:     name,
			ProxyURL: firstString(item, "proxy_url", "proxyUrl", "url"),
			Notes:    firstString(item, "notes", "note"),
		})
	}
	return result
}

func normalizeProxyGroupImportEntry(rawLine string, index int) proxyGroupImportItem {
	parts := strings.Split(rawLine, "|")
	switch len(parts) {
	case 2:
		return proxyGroupImportItem{
			Name:     strings.TrimSpace(parts[0]),
			ProxyURL: strings.TrimSpace(parts[1]),
		}
	case 3:
		return proxyGroupImportItem{
			ID:       strings.TrimSpace(parts[0]),
			Name:     strings.TrimSpace(parts[1]),
			ProxyURL: strings.TrimSpace(parts[2]),
		}
	default:
		return proxyGroupImportItem{
			ProxyURL: strings.TrimSpace(rawLine),
		}
	}
}

func firstString(item map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := item[key]; ok {
			if value := strings.TrimSpace(fmt.Sprint(raw)); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

func firstInt(item map[string]any, fallback int, keys ...string) int {
	for _, key := range keys {
		if raw, ok := item[key]; ok {
			switch value := raw.(type) {
			case float64:
				return int(value)
			case int:
				return value
			case int64:
				return int(value)
			case string:
				if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
					return parsed
				}
			default:
				if parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value))); err == nil {
					return parsed
				}
			}
		}
	}
	return fallback
}

func (h *Handler) handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	var successPtr *bool
	if raw := strings.TrimSpace(r.URL.Query().Get("success")); raw != "" {
		value := raw == "true" || raw == "1"
		successPtr = &value
	}

	entries := []appstats.RequestLogEntry{}
	if h.requestLogs != nil {
		entries = h.requestLogs.Query(appstats.RequestLogQuery{
			Limit:         limit,
			Offset:        offset,
			Protocol:      strings.TrimSpace(r.URL.Query().Get("protocol")),
			Endpoint:      strings.TrimSpace(r.URL.Query().Get("endpoint")),
			Model:         strings.TrimSpace(r.URL.Query().Get("model")),
			AccountID:     strings.TrimSpace(r.URL.Query().Get("account_id")),
			Success:       successPtr,
			FailureReason: strings.TrimSpace(r.URL.Query().Get("failure_reason")),
			BodySignal:    strings.TrimSpace(r.URL.Query().Get("body_signal")),
		})
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
	})
}

func (h *Handler) handleStatus(w http.ResponseWriter) {
	accountCount := 0
	activeCount := 0
	byStatus := map[string]int{
		"active":   0,
		"cooling":  0,
		"disabled": 0,
		"banned":   0,
	}
	if h.provider != nil {
		accounts := h.provider.SnapshotAccounts()
		accountCount = len(accounts)
		for _, account := range accounts {
			if !account.Disabled && account.Status == "active" {
				activeCount++
			}
			if _, ok := byStatus[string(account.Status)]; ok {
				byStatus[string(account.Status)]++
			}
		}
	}

	var snapshot appstats.Snapshot
	if h.stats != nil {
		snapshot = h.stats.Snapshot()
	}

	pool := provider.PoolSnapshot{}
	if h.provider != nil {
		pool = h.provider.PoolSnapshot()
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"accounts":  accountCount,
		"active":    activeCount,
		"by_status": byStatus,
		"status_descriptions": map[string]string{
			"active":   "可被调度的账号",
			"cooling":  "因网络或额度等原因暂时冷却中的账号",
			"disabled": "被管理员禁用或删除的账号",
			"banned":   "因风控或上游封禁被永久停用的账号",
		},
		"stats": snapshot,
		"pool":  pool,
	})
}

func (h *Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	var req provider.ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	accountSnapshot, err := h.provider.ImportAccount(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"account": accountSnapshotView(accountSnapshot),
	})
}

func (h *Handler) handleDisable(w http.ResponseWriter, r *http.Request, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.DisableAccount(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleEnable(w http.ResponseWriter, r *http.Request, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.EnableAccount(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleDelete(w http.ResponseWriter, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.DeleteAccount(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	accountSnapshot, err := h.provider.RefreshAccount(r.Context(), strings.TrimSpace(id))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"account": accountSnapshot,
	})
}

func (h *Handler) handleWeight(w http.ResponseWriter, r *http.Request, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		Weight int `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if err := h.provider.UpdateWeight(strings.TrimSpace(id), body.Weight); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleExport(w http.ResponseWriter) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"accounts": h.provider.ExportAccounts(),
	})
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	if h.catalog == nil {
		http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
		return
	}

	models, err := h.catalog.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"snapshot": h.catalog.Snapshot(),
		"models":   models,
	})
}

func (h *Handler) handleRefreshModels(w http.ResponseWriter, r *http.Request) {
	if h.catalog == nil {
		http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
		return
	}

	count, err := h.catalog.Refresh(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":  true,
		"count":    count,
		"snapshot": h.catalog.Snapshot(),
	})
}

func (h *Handler) handleWarmPool(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	pool, err := h.provider.WarmPool(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"pool":    pool,
	})
}

func (h *Handler) handleRefreshPool(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	pool, err := h.provider.RefreshPool(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"pool":    pool,
	})
}
