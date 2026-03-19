package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"kirocli-go/internal/domain/account"
)

func TestImportProxyGroupsAndAssignAccounts(t *testing.T) {
	provider, err := New(Config{
		Source:         "env",
		BearerToken:    "env-bearer",
		StatePath:      filepath.Join(t.TempDir(), "accounts_state.json"),
		ProxyStatePath: filepath.Join(t.TempDir(), "proxy_state.json"),
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	imported, err := provider.ImportProxyGroups(context.Background(), ProxyGroupImportRequest{
		Content: "demo:secret@74.81.81.81:10000\ndemo:secret@74.81.81.81:10001",
	})
	if err != nil {
		t.Fatalf("ImportProxyGroups error: %v", err)
	}
	if imported.Imported != 2 {
		t.Fatalf("expected 2 proxy groups, got %d", imported.Imported)
	}
	if !strings.HasPrefix(imported.Groups[0].ProxyURL, "http://") {
		t.Fatalf("expected proxy url to be normalized, got %s", imported.Groups[0].ProxyURL)
	}

	accountA, err := provider.ImportAccount(context.Background(), ImportRequest{
		ID:          "managed-a",
		BearerToken: "bearer-a",
	})
	if err != nil {
		t.Fatalf("ImportAccount A error: %v", err)
	}
	accountB, err := provider.ImportAccount(context.Background(), ImportRequest{
		ID:          "managed-b",
		BearerToken: "bearer-b",
	})
	if err != nil {
		t.Fatalf("ImportAccount B error: %v", err)
	}

	assignResult, err := provider.AssignProxyGroups(context.Background(), ProxyGroupAssignRequest{
		AccountIDs:       []string{accountA.ID, accountB.ID},
		ProxyGroupIDs:    []string{imported.Groups[0].ID},
		AccountsPerProxy: 2,
	})
	if err != nil {
		t.Fatalf("AssignProxyGroups error: %v", err)
	}
	if assignResult.Updated != 2 {
		t.Fatalf("expected 2 assigned accounts, got %d", assignResult.Updated)
	}
	if err := provider.DisableAccount("env-0"); err != nil {
		t.Fatalf("DisableAccount env-0 error: %v", err)
	}

	lease, err := provider.Acquire(context.Background(), account.AcquireHint{Profile: account.ProfileCLI})
	if err != nil {
		t.Fatalf("Acquire error: %v", err)
	}
	if got := lease.Metadata["proxy_group_id"]; got != imported.Groups[0].ID {
		t.Fatalf("expected proxy_group_id %s, got %s", imported.Groups[0].ID, got)
	}
	if !strings.HasPrefix(lease.Metadata["proxy_url"], "http://demo:secret@74.81.81.81:10000") {
		t.Fatalf("unexpected proxy_url metadata: %s", lease.Metadata["proxy_url"])
	}
}

func TestImportProxyGroupsSupportsStructuredGroups(t *testing.T) {
	disabled := false
	provider, err := New(Config{
		Source:         "env",
		BearerToken:    "env-bearer",
		StatePath:      filepath.Join(t.TempDir(), "accounts_state.json"),
		ProxyStatePath: filepath.Join(t.TempDir(), "proxy_state.json"),
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	result, err := provider.ImportProxyGroups(context.Background(), ProxyGroupImportRequest{
		Groups: []ProxyGroupImportItem{
			{
				ID:       "pool-us-01",
				Name:     "US Pool 01",
				ProxyURL: "demo:secret@74.81.81.81:10000",
				Enabled:  &disabled,
				Notes:    "accounts 1-5",
			},
		},
	})
	if err != nil {
		t.Fatalf("ImportProxyGroups error: %v", err)
	}
	if result.Imported != 1 {
		t.Fatalf("expected 1 imported proxy group, got %d", result.Imported)
	}
	group := result.Groups[0]
	if group.ID != "pool-us-01" {
		t.Fatalf("expected custom group id, got %s", group.ID)
	}
	if group.Name != "US Pool 01" {
		t.Fatalf("expected custom group name, got %s", group.Name)
	}
	if group.Enabled {
		t.Fatalf("expected imported group to stay disabled")
	}
	if group.Notes != "accounts 1-5" {
		t.Fatalf("expected notes to persist, got %s", group.Notes)
	}
	if !strings.HasPrefix(group.ProxyURL, "http://demo:secret@74.81.81.81:10000") {
		t.Fatalf("expected structured proxy url to be normalized, got %s", group.ProxyURL)
	}
}

func TestBulkImportSupportsCamelCaseRecords(t *testing.T) {
	oidcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken":  "bulk-bearer",
			"refreshToken": "",
			"expiresIn":    3600,
		})
	}))
	defer oidcServer.Close()

	provider, err := New(Config{
		Source:         "env",
		BearerToken:    "env-bearer",
		OIDCURL:        oidcServer.URL,
		StatePath:      filepath.Join(t.TempDir(), "accounts_state.json"),
		ProxyStatePath: filepath.Join(t.TempDir(), "proxy_state.json"),
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	proxies, err := provider.ImportProxyGroups(context.Background(), ProxyGroupImportRequest{
		Content: "demo:secret@74.81.81.81:10000",
	})
	if err != nil {
		t.Fatalf("ImportProxyGroups error: %v", err)
	}
	if proxies.Imported != 1 {
		t.Fatalf("expected 1 imported proxy group, got %d", proxies.Imported)
	}

	result, err := provider.ImportAccountsBulk(context.Background(), BulkImportRequest{
		DefaultWeight: 180,
		Content: `[
		  {
		    "id": "builder-1",
		    "clientId": "cid",
		    "clientSecret": "csecret",
		    "refreshToken": "rt"
		  }
		]`,
	})
	if err != nil {
		t.Fatalf("ImportAccountsBulk error: %v", err)
	}
	if result.Imported != 1 {
		t.Fatalf("expected 1 imported account, got %d", result.Imported)
	}
	if !result.Accounts[0].HasBearer {
		t.Fatalf("expected imported account to receive bearer token")
	}
}

func TestImportProxyGroupsSupportsStructuredItems(t *testing.T) {
	provider, err := New(Config{
		Source:         "env",
		BearerToken:    "env-bearer",
		StatePath:      filepath.Join(t.TempDir(), "accounts_state.json"),
		ProxyStatePath: filepath.Join(t.TempDir(), "proxy_state.json"),
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	disabled := false
	result, err := provider.ImportProxyGroups(context.Background(), ProxyGroupImportRequest{
		NamePrefix: "proxy",
		Groups: []ProxyGroupImportItem{{
			ID:       "proxy-us-01",
			Name:     "US-01",
			ProxyURL: "demo:secret@74.81.81.81:10000",
			Enabled:  &disabled,
			Notes:    "group-a",
		}},
	})
	if err != nil {
		t.Fatalf("ImportProxyGroups error: %v", err)
	}
	if result.Imported != 1 {
		t.Fatalf("expected 1 imported proxy group, got %d", result.Imported)
	}
	if got := result.Groups[0].ID; got != "proxy-us-01" {
		t.Fatalf("expected proxy group id proxy-us-01, got %s", got)
	}
	if got := result.Groups[0].Name; got != "US-01" {
		t.Fatalf("expected proxy group name US-01, got %s", got)
	}
	if result.Groups[0].Enabled {
		t.Fatalf("expected imported proxy group to remain disabled")
	}
	if !strings.HasPrefix(result.Groups[0].ProxyURL, "http://demo:secret@74.81.81.81:10000") {
		t.Fatalf("expected normalized proxy url, got %s", result.Groups[0].ProxyURL)
	}

	snapshots := provider.ListProxyGroups()
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 proxy group snapshot, got %d", len(snapshots))
	}
	if snapshots[0].Notes != "group-a" {
		t.Fatalf("expected notes to be preserved, got %s", snapshots[0].Notes)
	}
}
