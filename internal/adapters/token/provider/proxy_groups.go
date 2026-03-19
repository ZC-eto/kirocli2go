package provider

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"kirocli-go/internal/adapters/store/jsonfile"
	"kirocli-go/internal/adapters/upstream/clihttp"
)

type proxyStateFile struct {
	Groups []proxyGroupState `json:"groups,omitempty"`
}

type proxyGroupState struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProxyURL  string `json:"proxy_url"`
	Enabled   bool   `json:"enabled"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

type ProxyGroupSnapshot struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	ProxyURL          string `json:"proxy_url"`
	ProxyURLMasked    string `json:"proxy_url_masked"`
	Enabled           bool   `json:"enabled"`
	Notes             string `json:"notes,omitempty"`
	BoundAccountCount int    `json:"bound_account_count"`
	CreatedAt         int64  `json:"created_at,omitempty"`
	UpdatedAt         int64  `json:"updated_at,omitempty"`
}

type ProxyGroupImportItem struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	ProxyURL string `json:"proxy_url"`
	Enabled  *bool  `json:"enabled,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

type ProxyGroupImportRequest struct {
	Content    string                 `json:"content,omitempty"`
	NamePrefix string                 `json:"name_prefix,omitempty"`
	Groups     []ProxyGroupImportItem `json:"groups,omitempty"`
}

type ProxyGroupImportResult struct {
	Imported int                  `json:"imported"`
	Groups   []ProxyGroupSnapshot `json:"groups,omitempty"`
	Errors   []string             `json:"errors,omitempty"`
}

type ProxyGroupAssignRequest struct {
	AccountIDs       []string `json:"account_ids,omitempty"`
	ProxyGroupID     string   `json:"proxy_group_id,omitempty"`
	ProxyGroupIDs    []string `json:"proxy_group_ids,omitempty"`
	AccountsPerProxy int      `json:"accounts_per_proxy,omitempty"`
}

type ProxyGroupAssignResult struct {
	Updated  int               `json:"updated"`
	Accounts []AccountSnapshot `json:"accounts,omitempty"`
}

func (p *Provider) loadProxyState() error {
	if strings.TrimSpace(p.cfg.ProxyStatePath) == "" {
		return nil
	}

	var state proxyStateFile
	if err := jsonfile.Load(p.cfg.ProxyStatePath, &state); err != nil {
		return err
	}

	for _, group := range state.Groups {
		normalized, err := clihttp.NormalizeProxyURL(group.ProxyURL)
		if err != nil {
			continue
		}
		group.ProxyURL = normalized
		group.Name = strings.TrimSpace(group.Name)
		if group.Name == "" {
			group.Name = defaultProxyGroupName(normalized)
		}
		p.proxyGroups[group.ID] = &proxyGroupState{
			ID:        group.ID,
			Name:      group.Name,
			ProxyURL:  group.ProxyURL,
			Enabled:   group.Enabled,
			Notes:     group.Notes,
			CreatedAt: group.CreatedAt,
			UpdatedAt: group.UpdatedAt,
		}
	}

	return nil
}

func (p *Provider) saveProxyStateLocked() error {
	if strings.TrimSpace(p.cfg.ProxyStatePath) == "" {
		return nil
	}

	state := proxyStateFile{
		Groups: make([]proxyGroupState, 0, len(p.proxyGroups)),
	}
	for _, id := range p.sortedProxyGroupIDsLocked() {
		group := p.proxyGroups[id]
		state.Groups = append(state.Groups, *group)
	}

	return jsonfile.Save(p.cfg.ProxyStatePath, state)
}

func (p *Provider) ListProxyGroups() []ProxyGroupSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]ProxyGroupSnapshot, 0, len(p.proxyGroups))
	for _, id := range p.sortedProxyGroupIDsLocked() {
		result = append(result, p.proxySnapshotLocked(p.proxyGroups[id]))
	}
	return result
}

func (p *Provider) ImportProxyGroups(ctx context.Context, req ProxyGroupImportRequest) (ProxyGroupImportResult, error) {
	_ = ctx
	items := normalizeProxyGroupImportItems(req)

	p.mu.Lock()
	defer p.mu.Unlock()

	result := ProxyGroupImportResult{
		Groups: make([]ProxyGroupSnapshot, 0),
		Errors: make([]string, 0),
	}

	for idx, item := range items {
		proxyURL := strings.TrimSpace(item.ProxyURL)
		if proxyURL == "" {
			continue
		}
		normalized, err := clihttp.NormalizeProxyURL(proxyURL)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", proxyGroupImportLabel(item, idx), err))
			continue
		}

		targetID := strings.TrimSpace(item.ID)
		existingByURL := p.findProxyGroupByURLLocked(normalized)
		if existingByURL != nil && targetID != "" && existingByURL.ID != targetID {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: proxy url already exists as %s", proxyGroupImportLabel(item, idx), existingByURL.ID))
			continue
		}

		now := time.Now().Unix()
		group := existingByURL
		created := false
		if targetID != "" {
			existingByID, ok := p.proxyGroups[targetID]
			if ok {
				group = existingByID
			}
		}

		if group == nil {
			if targetID == "" {
				targetID = p.makeProxyGroupIDLocked(normalized)
			} else if _, exists := p.proxyGroups[targetID]; exists {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: proxy group id already exists", proxyGroupImportLabel(item, idx)))
				continue
			}

			enabled := true
			if item.Enabled != nil {
				enabled = *item.Enabled
			}
			group = &proxyGroupState{
				ID:        targetID,
				Enabled:   enabled,
				CreatedAt: now,
			}
			p.proxyGroups[group.ID] = group
			created = true
		} else if item.Enabled != nil {
			group.Enabled = *item.Enabled
		}

		group.ProxyURL = normalized
		if name := strings.TrimSpace(item.Name); name != "" {
			group.Name = name
		} else if strings.TrimSpace(group.Name) == "" {
			group.Name = buildProxyGroupName(req.NamePrefix, normalized)
			if group.Name == "" {
				group.Name = defaultProxyGroupName(normalized)
			}
		}
		if notes := strings.TrimSpace(item.Notes); notes != "" || created {
			group.Notes = notes
		}
		group.UpdatedAt = now

		result.Imported++
		result.Groups = append(result.Groups, p.proxySnapshotLocked(group))
	}

	if result.Imported == 0 && len(result.Errors) > 0 {
		return result, fmt.Errorf("no proxy groups imported")
	}
	if err := p.saveProxyStateLocked(); err != nil {
		return result, err
	}

	return result, nil
}

func (p *Provider) EnableProxyGroup(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	group, err := p.proxyGroupLocked(id)
	if err != nil {
		return err
	}
	group.Enabled = true
	group.UpdatedAt = time.Now().Unix()
	return p.saveProxyStateLocked()
}

func (p *Provider) DisableProxyGroup(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	group, err := p.proxyGroupLocked(id)
	if err != nil {
		return err
	}
	group.Enabled = false
	group.UpdatedAt = time.Now().Unix()
	return p.saveProxyStateLocked()
}

func (p *Provider) DeleteProxyGroup(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := p.proxyGroupLocked(id); err != nil {
		return err
	}
	if count := p.boundAccountCountLocked(id); count > 0 {
		return fmt.Errorf("proxy group %s is still bound to %d account(s)", id, count)
	}

	delete(p.proxyGroups, id)
	return p.saveProxyStateLocked()
}

func (p *Provider) AssignProxyGroups(ctx context.Context, req ProxyGroupAssignRequest) (ProxyGroupAssignResult, error) {
	_ = ctx
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(req.AccountIDs) == 0 {
		return ProxyGroupAssignResult{}, fmt.Errorf("account_ids is required")
	}

	var assigned []*tokenAccount
	switch {
	case strings.TrimSpace(req.ProxyGroupID) != "":
		groupID := strings.TrimSpace(req.ProxyGroupID)
		if _, err := p.proxyGroupLocked(groupID); err != nil {
			return ProxyGroupAssignResult{}, err
		}
		for _, accountID := range req.AccountIDs {
			item, err := p.findAccountLocked(strings.TrimSpace(accountID))
			if err != nil {
				return ProxyGroupAssignResult{}, err
			}
			item.ProxyGroupID = groupID
			assigned = append(assigned, item)
		}
	case len(req.ProxyGroupIDs) > 0:
		perProxy := req.AccountsPerProxy
		if perProxy <= 0 {
			perProxy = 1
		}
		groupIDs := make([]string, 0, len(req.ProxyGroupIDs))
		for _, rawID := range req.ProxyGroupIDs {
			groupID := strings.TrimSpace(rawID)
			if groupID == "" {
				continue
			}
			if _, err := p.proxyGroupLocked(groupID); err != nil {
				return ProxyGroupAssignResult{}, err
			}
			groupIDs = append(groupIDs, groupID)
		}
		if len(groupIDs) == 0 {
			return ProxyGroupAssignResult{}, fmt.Errorf("proxy_group_ids is required")
		}
		if len(req.AccountIDs) > len(groupIDs)*perProxy {
			return ProxyGroupAssignResult{}, fmt.Errorf("not enough proxy groups for %d accounts with accounts_per_proxy=%d", len(req.AccountIDs), perProxy)
		}

		for idx, accountID := range req.AccountIDs {
			item, err := p.findAccountLocked(strings.TrimSpace(accountID))
			if err != nil {
				return ProxyGroupAssignResult{}, err
			}
			groupID := groupIDs[idx/perProxy]
			item.ProxyGroupID = groupID
			assigned = append(assigned, item)
		}
	default:
		return ProxyGroupAssignResult{}, fmt.Errorf("proxy_group_id or proxy_group_ids is required")
	}

	if err := p.saveStateLocked(); err != nil {
		return ProxyGroupAssignResult{}, err
	}

	result := ProxyGroupAssignResult{
		Updated:  len(assigned),
		Accounts: make([]AccountSnapshot, 0, len(assigned)),
	}
	for _, item := range assigned {
		result.Accounts = append(result.Accounts, p.snapshotFromTokenAccount(item))
	}
	return result, nil
}

func (p *Provider) resolveProxyForAccountLocked(item *tokenAccount) (string, *proxyGroupState, error) {
	if strings.TrimSpace(item.ProxyGroupID) != "" {
		group, err := p.proxyGroupLocked(item.ProxyGroupID)
		if err != nil {
			return "", nil, err
		}
		if !group.Enabled {
			return "", nil, fmt.Errorf("proxy group %s is disabled", item.ProxyGroupID)
		}
		return group.ProxyURL, group, nil
	}

	normalized, err := clihttp.NormalizeProxyURL(p.cfg.ProxyURL)
	if err != nil {
		return "", nil, err
	}
	return normalized, nil, nil
}

func (p *Provider) proxySnapshotLocked(group *proxyGroupState) ProxyGroupSnapshot {
	return ProxyGroupSnapshot{
		ID:                group.ID,
		Name:              group.Name,
		ProxyURL:          group.ProxyURL,
		ProxyURLMasked:    maskProxyURL(group.ProxyURL),
		Enabled:           group.Enabled,
		Notes:             group.Notes,
		BoundAccountCount: p.boundAccountCountLocked(group.ID),
		CreatedAt:         group.CreatedAt,
		UpdatedAt:         group.UpdatedAt,
	}
}

func (p *Provider) proxyGroupLocked(id string) (*proxyGroupState, error) {
	group, ok := p.proxyGroups[strings.TrimSpace(id)]
	if !ok {
		return nil, fmt.Errorf("proxy group %s not found", id)
	}
	return group, nil
}

func (p *Provider) findProxyGroupByURLLocked(proxyURL string) *proxyGroupState {
	for _, group := range p.proxyGroups {
		if group.ProxyURL == proxyURL {
			return group
		}
	}
	return nil
}

func (p *Provider) makeProxyGroupIDLocked(proxyURL string) string {
	base := "proxy"
	if parsed, err := url.Parse(proxyURL); err == nil {
		host := strings.NewReplacer(".", "-", ":", "-", "_", "-").Replace(parsed.Host)
		host = strings.Trim(host, "-")
		if host != "" {
			base = host
		}
	}

	id := base
	for idx := 1; ; idx++ {
		if _, exists := p.proxyGroups[id]; !exists {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, idx+1)
	}
}

func (p *Provider) sortedProxyGroupIDsLocked() []string {
	ids := make([]string, 0, len(p.proxyGroups))
	for id := range p.proxyGroups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (p *Provider) boundAccountCountLocked(proxyGroupID string) int {
	count := 0
	for _, item := range p.accounts {
		if item.ProxyGroupID == proxyGroupID {
			count++
		}
	}
	return count
}

func buildProxyGroupName(prefix, proxyURL string) string {
	base := defaultProxyGroupName(proxyURL)
	if strings.TrimSpace(prefix) == "" {
		return base
	}
	return strings.TrimSpace(prefix) + "-" + base
}

func defaultProxyGroupName(proxyURL string) string {
	parsed, err := url.Parse(proxyURL)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return "proxy-group"
	}
	return parsed.Host
}

func maskProxyURL(proxyURL string) string {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return proxyURL
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if username != "" {
			parsed.User = url.UserPassword(username, "***")
		}
	}
	return parsed.String()
}

func normalizeProxyGroupImportItems(req ProxyGroupImportRequest) []ProxyGroupImportItem {
	if len(req.Groups) > 0 {
		items := make([]ProxyGroupImportItem, 0, len(req.Groups))
		for _, item := range req.Groups {
			if strings.TrimSpace(item.ProxyURL) == "" {
				continue
			}
			items = append(items, ProxyGroupImportItem{
				ID:       strings.TrimSpace(item.ID),
				Name:     strings.TrimSpace(item.Name),
				ProxyURL: strings.TrimSpace(item.ProxyURL),
				Enabled:  item.Enabled,
				Notes:    strings.TrimSpace(item.Notes),
			})
		}
		return items
	}

	lines := strings.Split(strings.ReplaceAll(req.Content, "\r", ""), "\n")
	items := make([]ProxyGroupImportItem, 0, len(lines))
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		items = append(items, parseProxyGroupImportLine(line))
	}
	return items
}

func parseProxyGroupImportLine(line string) ProxyGroupImportItem {
	parts := strings.Split(line, "|")
	switch len(parts) {
	case 2:
		return ProxyGroupImportItem{
			Name:     strings.TrimSpace(parts[0]),
			ProxyURL: strings.TrimSpace(parts[1]),
		}
	case 3:
		return ProxyGroupImportItem{
			ID:       strings.TrimSpace(parts[0]),
			Name:     strings.TrimSpace(parts[1]),
			ProxyURL: strings.TrimSpace(parts[2]),
		}
	default:
		return ProxyGroupImportItem{ProxyURL: line}
	}
}

func proxyGroupImportLabel(item ProxyGroupImportItem, idx int) string {
	if value := strings.TrimSpace(item.ID); value != "" {
		return value
	}
	if value := strings.TrimSpace(item.Name); value != "" {
		return value
	}
	if value := strings.TrimSpace(item.ProxyURL); value != "" {
		return value
	}
	return fmt.Sprintf("item %d", idx+1)
}
