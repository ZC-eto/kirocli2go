package clihttp

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type ClientPool struct {
	timeout time.Duration

	mu      sync.Mutex
	clients map[string]*http.Client
}

func NewClientPool(timeout time.Duration) *ClientPool {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &ClientPool{
		timeout: timeout,
		clients: make(map[string]*http.Client),
	}
}

func (p *ClientPool) ClientForProxy(proxyURL string) (*http.Client, error) {
	normalized, err := NormalizeProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if client, ok := p.clients[normalized]; ok {
		return client, nil
	}

	client := &http.Client{
		Timeout:   p.timeout,
		Transport: NewTransport(Config{ProxyURL: normalized}),
	}
	p.clients[normalized] = client
	return client, nil
}

func NormalizeProxyURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}

	candidate := raw
	if !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
	}

	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Scheme) == "" {
		return "", fmt.Errorf("proxy scheme is required")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("proxy host is required")
	}

	return parsed.String(), nil
}
