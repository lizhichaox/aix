package internal

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestHandleHealth(t *testing.T) {
	cfg := DefaultProxyConfig()
	ps := NewProxyServer(cfg)

	w := newMockResponseWriter()
	r, _ := http.NewRequest("GET", "/health", nil)
	ps.handleHealth(w, r)

	if w.statusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", w.statusCode)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}
	if resp["listen"] != cfg.Listen {
		t.Errorf("listen = %v, want %s", resp["listen"], cfg.Listen)
	}
	if _, ok := resp["today_usage"]; ok {
		t.Error("health response must not expose usage")
	}
}

type mockResponseWriter struct {
	statusCode int
	body       *bytes.Buffer
	header     http.Header
	flushes    int
}

func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{body: new(bytes.Buffer), header: make(http.Header)}
}

func (m *mockResponseWriter) Header() http.Header { return m.header }
func (m *mockResponseWriter) Write(b []byte) (int, error) {
	if m.statusCode == 0 {
		m.statusCode = http.StatusOK
	}
	return m.body.Write(b)
}
func (m *mockResponseWriter) WriteHeader(code int) { m.statusCode = code }
func (m *mockResponseWriter) Flush()               { m.flushes++ }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNewProxyServer_WithProxy(t *testing.T) {
	cfg := DefaultProxyConfig()
	cfg.Proxy = "http://127.0.0.1:6152"
	ps := NewProxyServer(cfg)

	if ps.client.Transport == nil {
		t.Fatal("expected Transport to be set when proxy is configured")
	}
}

func TestNewProxyServer_WithoutProxy(t *testing.T) {
	cfg := DefaultProxyConfig()
	cfg.Proxy = ""
	ps := NewProxyServer(cfg)

	if ps.client.Transport != nil {
		t.Fatal("expected Transport to be nil when proxy is not configured")
	}
}

func TestNewProxyServer_InvalidProxy(t *testing.T) {
	cfg := DefaultProxyConfig()
	cfg.Proxy = "://invalid"
	ps := NewProxyServer(cfg)

	if ps == nil {
		t.Fatal("server should be created even with invalid proxy URL")
	}
	if ps.client.Transport != nil {
		t.Fatal("expected Transport to be nil for invalid proxy URL")
	}
}

func TestDetectProvider_MultiChatCompletions(t *testing.T) {
	cfg := &ProxyConfig{
		GatewayKey: "test-key",
		Providers: map[string]*ProviderConfig{
			"deepseek": {
				Name:     "deepseek",
				Upstream: "https://api.deepseek.com",
				Models:   map[string]string{"deepseek-v4-pro": "deepseek-v4-pro"},
			},
			"kimi": {
				Name:     "kimi",
				Upstream: "https://api.moonshot.ai",
				Models:   map[string]string{"kimi-k2.7-code": "kimi-k2.7-code"},
			},
		},
	}

	// Request with model kimi-k2.7-code → should route to kimi provider
	body := `{"model":"kimi-k2.7-code","messages":[{"role":"user","content":"hi"}]}`
	r, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/chat/completions", bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", "application/json")

	ps := NewProxyServer(cfg)
	provider := ps.detectProvider(r, "")
	if provider == nil {
		t.Fatal("expected non-nil provider")
	}
	if provider.Name != "kimi" {
		t.Errorf("wanted kimi provider, got %s", provider.Name)
	}

	// Request with model deepseek-v4-pro → should route to deepseek
	body2 := `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hi"}]}`
	r2, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/chat/completions", bytes.NewReader([]byte(body2)))
	r2.Header.Set("Content-Type", "application/json")
	provider2 := ps.detectProvider(r2, "")
	if provider2 == nil {
		t.Fatal("expected non-nil provider")
	}
	if provider2.Name != "deepseek" {
		t.Errorf("wanted deepseek provider, got %s", provider2.Name)
	}
}

func TestDetectProvider_MessagesAPI(t *testing.T) {
	cfg := &ProxyConfig{
		GatewayKey: "test-key",
		Providers: map[string]*ProviderConfig{
			"deepseek-anthropic": {
				Name:     "deepseek",
				Upstream: "https://api.deepseek.com/anthropic",
				Models:   map[string]string{"claude-sonnet-4-6": "deepseek-v4-pro"},
			},
		},
	}

	r, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/messages", nil)
	ps := NewProxyServer(cfg)
	provider := ps.detectProvider(r, "")
	if provider == nil {
		t.Fatal("expected non-nil provider for /v1/messages")
	}
}

func TestDetectProvider_MessagesAPIPrefersAnthropicFlag(t *testing.T) {
	cfg := &ProxyConfig{
		Providers: map[string]*ProviderConfig{
			"kimi": {
				Name:     "kimi",
				Upstream: "https://api.moonshot.ai",
				Models:   map[string]string{"kimi-k2.7-code": "kimi-k2.7-code"},
			},
			"openrouter": {
				Name:            "OpenRouter",
				Upstream:        "https://openrouter.ai/api",
				Models:          map[string]string{"claude-opus-5": "anthropic/claude-opus-5"},
				AnthropicNative: true,
			},
		},
	}
	r, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/messages", nil)
	ps := NewProxyServer(cfg)
	provider := ps.detectProvider(r, "")
	if provider == nil || provider.Name != "OpenRouter" {
		t.Fatalf("messages should route to the anthropic-flagged provider, got %+v", provider)
	}
}

func TestExtractProviderPrefixAnthropicUpstreams(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"openrouter":   {Name: "OpenRouter", Upstream: "https://openrouter.ai/api"},
		"opencode-zen": {Name: "OpenCode Zen", Upstream: "https://opencode.ai/zen"},
	}}
	ps := NewProxyServer(cfg)
	name, stripped := ps.extractProviderPrefix("/openrouter/v1/messages")
	if name != "openrouter" || stripped != "/v1/messages" {
		t.Fatalf("extractProviderPrefix = (%q, %q), want (openrouter, /v1/messages)", name, stripped)
	}
	for upstream, want := range map[string]string{
		"https://openrouter.ai/api":  "https://openrouter.ai/api/v1/messages",
		"https://opencode.ai/zen":    "https://opencode.ai/zen/v1/messages",
		"https://opencode.ai/zen/go": "https://opencode.ai/zen/go/v1/messages",
	} {
		if got := singleJoinSlash(upstream, "/v1/messages"); got != want {
			t.Errorf("singleJoinSlash(%q) = %q, want %q", upstream, got, want)
		}
	}
}

func TestExtractModelFromBody(t *testing.T) {
	body := `{"model":"gpt-5.5","messages":[]}`
	r, _ := http.NewRequest("POST", "/v1", bytes.NewReader([]byte(body)))
	model := extractModelFromBody(r)
	if model != "gpt-5.5" {
		t.Errorf("model = %q, want gpt-5.5", model)
	}

	// Body should be restored
	data, _ := io.ReadAll(r.Body)
	if !bytes.Contains(data, []byte("gpt-5.5")) {
		t.Error("body not restored")
	}
}

func TestRequestHarness(t *testing.T) {
	cases := map[string]string{
		"/v1/messages":                             HarnessClaude,
		"/opencode-go/v1/messages":                 HarnessClaude,
		"/opencode-go/v1/messages/count_tokens":    HarnessClaude,
		"/v1/responses":                            HarnessCodex,
		"/codex-opencode-go/v1/responses":          HarnessCodex,
		"/codex-opencode-go/v1/responses/compact":  HarnessCodex,
		"/codex-opencode-go/v1/responses/resp_123": HarnessCodex,
		"/codex-opencode-go/v1/models":             HarnessCodex,
		"/v1/chat/completions":                     "unknown",
		"/health":                                  "unknown",
	}
	for path, want := range cases {
		if got := requestHarness(path); got != want {
			t.Errorf("requestHarness(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRequestClient(t *testing.T) {
	cases := []struct {
		path string
		ua   string
		want string
	}{
		{"/v1/messages", "claude-cli/2.1.223", "claude-code"},
		{"/v1/messages", "Mozilla/5.0 Claude/1.0 Electron/38", "claude-desktop"},
		{"/v1/messages", "claude-cli/2.1.237 (external, claude-desktop-3p, agent-sdk/0.3.237)", "claude-code"},
		{"/v1/messages", "Mozilla/5.0 (Macintosh) Claude/1.37937.0 Chrome/148 Electron/42.10.0", "claude-desktop"},
		{"/v1/responses", "codex-cli/0.1", "codex-cli"},
		{"/codex-opencode-go/v1/models", "Mozilla/5.0 ChatGPT Electron/38", "codex-desktop"},
		{"/v1/responses", "Codex Desktop/0.149.0-alpha.4.3 (Mac OS 26.6.2; arm64) ghostty/1.3.1 (Codex Desktop; 26.818.61809)", "codex-desktop"},
		{"/health", "curl/8.7.1", "curl"},
		{"/health", "", "unknown-client"},
	}
	for _, tc := range cases {
		r, _ := http.NewRequest("GET", tc.path, nil)
		r.Header.Set("User-Agent", tc.ua)
		if got := requestClient(r, requestHarness(tc.path)); got != tc.want {
			t.Errorf("requestClient(%q, %q) = %q, want %q", tc.path, tc.ua, got, tc.want)
		}
	}
}

func TestHandleRequestResponsesPassthrough(t *testing.T) {
	cfg := &ProxyConfig{
		GatewayKey: "gateway-key",
		Providers: map[string]*ProviderConfig{
			"codex-opencode-go": {
				Name:      "OpenCode Go-Responses",
				Upstream:  "https://opencode.ai/zen/go",
				AuthToken: "upstream-key",
				Models:    map[string]string{"client-model": "upstream-model"},
			},
		},
	}
	ps := NewProxyServer(cfg)
	ps.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); got != "https://opencode.ai/zen/go/v1/responses?trace=1" {
			t.Errorf("upstream URL = %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer upstream-key" {
			t.Errorf("upstream Authorization = %q", got)
		}
		body, _ := io.ReadAll(req.Body)
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "upstream-model" {
			t.Errorf("rewritten payload = %#v", payload)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(bytes.NewBufferString("data: {\"type\":\"response.completed\"}\n\n")),
		}, nil
	})}

	w := newMockResponseWriter()
	body := `{"model":"client-model","reasoning":{"effort":"high"},"input":"hello"}`
	r, _ := http.NewRequest("POST", "http://127.0.0.1:2026/codex-opencode-go/v1/responses?trace=1", bytes.NewBufferString(body))
	r.Header.Set("Authorization", "Bearer gateway-key")
	ps.handleRequest(w, r)

	if w.statusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.statusCode, w.body.String())
	}
	if !bytes.Contains(w.body.Bytes(), []byte("response.completed")) {
		t.Errorf("streaming response was not passed through: %q", w.body.String())
	}
	if w.flushes == 0 {
		t.Error("streaming response was not flushed")
	}
}

func TestHandleRequestResponsesRequiresExplicitProvider(t *testing.T) {
	cfg := &ProxyConfig{GatewayKey: "gateway-key", Providers: map[string]*ProviderConfig{
		"codex-opencode-go": {Name: "OpenCode Go", Upstream: "https://example.invalid"},
	}}
	ps := NewProxyServer(cfg)
	w := newMockResponseWriter()
	r, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/responses", bytes.NewBufferString(`{"model":"m"}`))
	r.Header.Set("Authorization", "Bearer gateway-key")
	ps.handleRequest(w, r)
	if w.statusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.statusCode)
	}
}

func TestExtractModelFromBody_Empty(t *testing.T) {
	r, _ := http.NewRequest("POST", "/v1", nil)
	model := extractModelFromBody(r)
	if model != "" {
		t.Errorf("model = %q, want empty", model)
	}
}

type errAfterReadCloser struct {
	data   []byte
	offset int
	err    error
}

func (e *errAfterReadCloser) Read(p []byte) (int, error) {
	if e.offset >= len(e.data) {
		return 0, e.err
	}
	n := copy(p, e.data[e.offset:])
	e.offset += n
	return n, nil
}

func (e *errAfterReadCloser) Close() error { return nil }

func TestExtractModelFromBody_ReadErrorRestoresBody(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","messages":[]}`)
	r, _ := http.NewRequest("POST", "/v1", &errAfterReadCloser{data: body, err: io.ErrUnexpectedEOF})
	model := extractModelFromBody(r)
	if model != "" {
		t.Errorf("model = %q, want empty on read error", model)
	}
	// Body should still be restored
	data, _ := io.ReadAll(r.Body)
	if !bytes.Equal(data, body) {
		t.Errorf("restored body = %q, want %q", data, body)
	}
}

func TestDetectProvider_FallbackDeterministic(t *testing.T) {
	cfg := &ProxyConfig{
		GatewayKey: "test-key",
		Providers: map[string]*ProviderConfig{
			"beta": {
				Name:     "beta",
				Upstream: "https://beta.example.com",
				Models:   map[string]string{"gpt-5.5": "beta-model"},
			},
			"alpha": {
				Name:     "alpha",
				Upstream: "https://alpha.example.com",
				Models:   map[string]string{"gpt-4": "alpha-model"},
			},
		},
	}
	ps := NewProxyServer(cfg)

	// Model doesn't match any provider; fallback should always pick "alpha"
	// (sorted first among non-anthropic providers).
	body := `{"model":"unknown-model","messages":[{"role":"user","content":"hi"}]}`
	var first string
	for i := 0; i < 20; i++ {
		r, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/chat/completions", bytes.NewReader([]byte(body)))
		provider := ps.detectProvider(r, "")
		if provider == nil {
			t.Fatal("expected non-nil provider")
		}
		if i == 0 {
			first = provider.Name
		} else if provider.Name != first {
			t.Fatalf("fallback provider flapped: got %s, want %s", provider.Name, first)
		}
	}
	if first != "alpha" {
		t.Errorf("fallback provider = %s, want alpha", first)
	}
}

func TestHandleModels_StableOrder(t *testing.T) {
	cfg := &ProxyConfig{
		GatewayKey: "test-key",
		Providers: map[string]*ProviderConfig{
			"kimi": {
				Name:     "kimi",
				Upstream: "https://api.moonshot.ai",
				Models:   map[string]string{"kimi-k2.7-code": "kimi-k2.7-code"},
			},
			"deepseek": {
				Name:     "deepseek",
				Upstream: "https://api.deepseek.com",
				Models:   map[string]string{"deepseek-v4-pro": "deepseek-v4-pro"},
			},
		},
	}
	ps := NewProxyServer(cfg)

	w := newMockResponseWriter()
	r, _ := http.NewRequest("GET", "/v1/models", nil)
	ps.handleModels(w, r, requestHarness(r.URL.Path), requestClient(r, requestHarness(r.URL.Path)))

	if w.statusCode != http.StatusOK && w.statusCode != 0 {
		t.Fatalf("status = %d, want 200", w.statusCode)
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("want 2 unique models, got %d", len(resp.Data))
	}
	// Providers and models are sorted alphabetically.
	if resp.Data[0].ID != "deepseek-v4-pro" {
		t.Errorf("first model id = %q, want deepseek-v4-pro", resp.Data[0].ID)
	}
	if resp.Data[1].ID != "kimi-k2.7-code" {
		t.Errorf("second model id = %q, want kimi-k2.7-code", resp.Data[1].ID)
	}
}

func TestExtractProviderPrefix(t *testing.T) {
	cfg := &ProxyConfig{
		Providers: map[string]*ProviderConfig{
			"openai": {Name: "openai", Upstream: "https://api.deepseek.com"},
			"kimi":   {Name: "kimi", Upstream: "https://api.moonshot.ai"},
		},
	}
	ps := NewProxyServer(cfg)

	cases := []struct {
		path         string
		wantName     string
		wantStripped string
	}{
		{"/openai/v1/responses", "openai", "/v1/responses"},
		{"/kimi/v1/chat/completions", "kimi", "/v1/chat/completions"},
		{"/openai/v1/models", "openai", "/v1/models"},
		{"/v1/responses", "", "/v1/responses"},
		{"/unknown/v1/responses", "", "/unknown/v1/responses"},
		{"/openai", "", "/openai"},
	}

	for _, tc := range cases {
		name, stripped := ps.extractProviderPrefix(tc.path)
		if name != tc.wantName || stripped != tc.wantStripped {
			t.Errorf("extractProviderPrefix(%q) = (%q, %q), want (%q, %q)",
				tc.path, name, stripped, tc.wantName, tc.wantStripped)
		}
	}
}

func TestHandleRequest_RoutePrefixModels(t *testing.T) {
	cfg := &ProxyConfig{
		GatewayKey: "aix-gateway",
		Providers: map[string]*ProviderConfig{
			"openai": {
				Name:     "openai",
				Upstream: "https://api.deepseek.com",
				Models:   map[string]string{"gpt-5.5": "deepseek-v4-pro"},
			},
			"kimi": {
				Name:     "kimi",
				Upstream: "https://api.moonshot.ai",
				Models:   map[string]string{"kimi-k2": "kimi-k2.7-code"},
			},
		},
	}
	ps := NewProxyServer(cfg)

	w := newMockResponseWriter()
	r, _ := http.NewRequest("GET", "http://127.0.0.1:2026/openai/v1/models", nil)
	r.Header.Set("Authorization", "Bearer aix-gateway")
	ps.handleRequest(w, r)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("want 1 model, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != "gpt-5.5" {
		t.Errorf("model id = %q, want gpt-5.5", resp.Data[0].ID)
	}
}

func TestExtractProviderPrefix_AmbiguousModel(t *testing.T) {
	cfg := &ProxyConfig{
		GatewayKey: "aix-gateway",
		Providers: map[string]*ProviderConfig{
			"openai": {
				Name:     "openai",
				Upstream: "https://api.deepseek.com",
				Models:   map[string]string{"gpt-5.5": "deepseek-v4-pro"},
			},
			"kimi": {
				Name:     "kimi",
				Upstream: "https://api.moonshot.ai",
				Models:   map[string]string{"gpt-5.5": "kimi-k2.7-code"},
			},
		},
	}
	ps := NewProxyServer(cfg)

	// When both providers map gpt-5.5, route prefix should force kimi selection.
	name, stripped := ps.extractProviderPrefix("/kimi/v1/responses")
	if name != "kimi" || stripped != "/v1/responses" {
		t.Fatalf("extractProviderPrefix(/kimi/v1/responses) = (%q, %q), want (kimi, /v1/responses)", name, stripped)
	}
}

func TestMaskHeader_AllLengths(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"a", "***"},
		{"ab", "***"},
		{"abc", "abc***"},
		{"abc12345678", "abc***5678"}, // >10: first 3 + *** + last 4
		{"abcdef", "abc***"},
	}
	for _, tc := range cases {
		got := MaskHeader(tc.input)
		if got != tc.want {
			t.Errorf("MaskHeader(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSingleJoinSlash(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"http://example.com", "/v1/chat", "http://example.com/v1/chat"},
		{"http://example.com/", "/v1/chat", "http://example.com/v1/chat"},
		{"http://example.com", "v1/chat", "http://example.com/v1/chat"},
		{"http://example.com/", "v1/chat", "http://example.com/v1/chat"},
		{"http://example.com/a/", "/b/", "http://example.com/a/b/"},
	}
	for _, tc := range cases {
		got := singleJoinSlash(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("singleJoinSlash(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestInjectAuth(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"deepseek": {Name: "deepseek", Upstream: "https://api.deepseek.com", AuthToken: "sk-test123"},
	}}
	ps := NewProxyServer(cfg)

	// Messages API uses x-api-key
	req1, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/messages", nil)
	ps.injectAuth(req1, cfg.Providers["deepseek"])
	if req1.Header.Get("x-api-key") != "sk-test123" {
		t.Errorf("x-api-key = %q, want sk-test123", req1.Header.Get("x-api-key"))
	}

	// Other paths use Authorization Bearer
	req2, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/chat/completions", nil)
	ps.injectAuth(req2, cfg.Providers["deepseek"])
	if req2.Header.Get("Authorization") != "Bearer sk-test123" {
		t.Errorf("Authorization = %q, want Bearer sk-test123", req2.Header.Get("Authorization"))
	}

	// Empty auth token clears headers
	cfg2 := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"noauth": {Name: "noauth", Upstream: "https://example.com", AuthToken: ""},
	}}
	ps2 := NewProxyServer(cfg2)
	req3, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/chat/completions", nil)
	req3.Header.Set("Authorization", "Bearer old-token")
	ps2.injectAuth(req3, cfg2.Providers["noauth"])
	if req3.Header.Get("Authorization") != "" {
		t.Errorf("Authorization should be cleared, got %q", req3.Header.Get("Authorization"))
	}
}

func TestRewriteModel(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"deepseek": {
			Name: "deepseek", Upstream: "https://api.deepseek.com",
			Models: map[string]string{"gpt-5.5": "deepseek-v4-pro"},
		},
	}}
	ps := NewProxyServer(cfg)

	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`)
	rewritten := ps.rewriteModel(body, cfg.Providers["deepseek"], "unknown")
	if !bytes.Contains(rewritten, []byte(`"deepseek-v4-pro"`)) {
		t.Errorf("model not rewritten, body=%s", rewritten)
	}
}

func TestRewriteModel_NoMatch(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"deepseek": {
			Name: "deepseek", Upstream: "https://api.deepseek.com",
			Models: map[string]string{"gpt-5.5": "deepseek-v4-pro"},
		},
	}}
	ps := NewProxyServer(cfg)

	body := []byte(`{"model":"unknown-model","messages":[]}`)
	rewritten := ps.rewriteModel(body, cfg.Providers["deepseek"], "unknown")
	if !bytes.Contains(rewritten, []byte(`"unknown-model"`)) {
		t.Errorf("model should be unchanged, body=%s", rewritten)
	}
}

func TestRewriteModel_OneMillionSuffix(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"deepseek": {
			Name: "deepseek", Upstream: "https://api.deepseek.com",
			Models: map[string]string{"claude-opus-5": "deepseek-v4-flash"},
		},
	}}
	ps := NewProxyServer(cfg)

	body := []byte(`{"model":"claude-opus-5[1m]","messages":[{"role":"user","content":"hi"}]}`)
	rewritten := ps.rewriteModel(body, cfg.Providers["deepseek"], "unknown")
	if !bytes.Contains(rewritten, []byte(`"deepseek-v4-flash"`)) {
		t.Errorf("1M variant should normalize to the base mapping, body=%s", rewritten)
	}
	if bytes.Contains(rewritten, []byte("[1m]")) {
		t.Errorf("1M suffix must not reach the upstream, body=%s", rewritten)
	}
}

func TestRewriteModel_OneMillionSuffix_ExplicitMappingWins(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"deepseek": {
			Name: "deepseek", Upstream: "https://api.deepseek.com",
			Models: map[string]string{"claude-opus-5[1m]": "deepseek-v4-pro"},
		},
	}}
	ps := NewProxyServer(cfg)

	body := []byte(`{"model":"claude-opus-5[1m]","messages":[]}`)
	rewritten := ps.rewriteModel(body, cfg.Providers["deepseek"], "unknown")
	if !bytes.Contains(rewritten, []byte(`"deepseek-v4-pro"`)) {
		t.Errorf("explicit [1m] mapping should win over suffix normalization, body=%s", rewritten)
	}
}

func TestRewriteModel_EmptyBody(t *testing.T) {
	ps := NewProxyServer(DefaultProxyConfig())
	p := &ProviderConfig{Models: map[string]string{"a": "b"}}
	rewritten := ps.rewriteModel([]byte{}, p, "unknown")
	if len(rewritten) != 0 {
		t.Errorf("empty body should stay empty, got %s", rewritten)
	}
}

func TestRewriteModel_NoModels(t *testing.T) {
	ps := NewProxyServer(DefaultProxyConfig())
	p := &ProviderConfig{}
	body := []byte(`{"model":"test"}`)
	rewritten := ps.rewriteModel(body, p, "unknown")
	if !bytes.Contains(rewritten, []byte(`"test"`)) {
		t.Errorf("body unchanged when provider has no models")
	}
}

func TestRewriteModel_IdentityMappingLogsNothing(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"opencode-go": {
			Name: "opencode-go", Upstream: "https://opencode.ai/zen/go",
			Models: map[string]string{"deepseek-v4-flash-vision-exp": "deepseek-v4-flash-vision-exp"},
		},
	}}
	ps := NewProxyServer(cfg)

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	body := []byte(`{"model":"deepseek-v4-flash-vision-exp","messages":[]}`)
	rewritten := ps.rewriteModel(body, cfg.Providers["opencode-go"], HarnessCodex)
	if !bytes.Contains(rewritten, []byte(`"deepseek-v4-flash-vision-exp"`)) {
		t.Errorf("identity mapping should keep the model, body=%s", rewritten)
	}
	if strings.Contains(buf.String(), "model:") {
		t.Errorf("identity mapping should not log a model rewrite, got: %s", buf.String())
	}
}

func TestRewriteModel_RealRewriteLogs(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"deepseek": {
			Name: "deepseek", Upstream: "https://api.deepseek.com",
			Models: map[string]string{"gpt-5.5": "deepseek-v4-pro"},
		},
	}}
	ps := NewProxyServer(cfg)

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	body := []byte(`{"model":"gpt-5.5","messages":[]}`)
	_ = ps.rewriteModel(body, cfg.Providers["deepseek"], HarnessCodex)
	if !strings.Contains(buf.String(), "[codex] [aix-proxy] model: gpt-5.5 → deepseek-v4-pro") {
		t.Errorf("real rewrite should log the mapping, got: %s", buf.String())
	}
}

func TestExtractModelFromBytes(t *testing.T) {
	model := extractModelFromBytes([]byte(`{"model":"gpt-5.5","messages":[]}`))
	if model != "gpt-5.5" {
		t.Errorf("model = %q, want gpt-5.5", model)
	}
}

func TestExtractModelFromBytes_Invalid(t *testing.T) {
	model := extractModelFromBytes([]byte(`{bad`))
	if model != "" {
		t.Errorf("model = %q, want empty", model)
	}
}

func TestSetReasoningEffort_PinsEffort(t *testing.T) {
	body := []byte(`{"model":"x","reasoning":{"effort":"medium"}}`)
	got := setReasoningEffort(body, "high")
	if !bytes.Contains(got, []byte(`"effort":"high"`)) {
		t.Errorf("effort not pinned, body=%s", got)
	}
	if bytes.Contains(got, []byte("medium")) {
		t.Errorf("original effort leaked, body=%s", got)
	}
}

func TestSetReasoningEffort_AddsWhenMissing(t *testing.T) {
	body := []byte(`{"model":"x"}`)
	got := setReasoningEffort(body, "high")
	if !bytes.Contains(got, []byte(`"effort":"high"`)) {
		t.Errorf("effort should be added, body=%s", got)
	}
}

func TestSetReasoningEffort_EmptyEffortNoop(t *testing.T) {
	body := []byte(`{"model":"x","reasoning":{"effort":"medium"}}`)
	got := setReasoningEffort(body, "")
	if !bytes.Contains(got, []byte("medium")) {
		t.Errorf("empty effort should leave body untouched, body=%s", got)
	}
}

func TestSetReasoningEffort_InvalidBodyNoop(t *testing.T) {
	body := []byte(`{bad`)
	got := setReasoningEffort(body, "high")
	if string(got) != string(body) {
		t.Errorf("invalid body should be untouched, got=%s", got)
	}
}

func TestIsGatewayReady_PrefersHealthFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ProxyHealth{Status: "ok"})
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultProxyConfig()
	cfg.Listen = addr
	if err := WriteProxyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// A fresh temp HOME has no PID file; only the health endpoint can attest.
	RemovePidFile()
	if !IsGatewayReady() {
		t.Errorf("IsGatewayReady = false, want true via health endpoint %s", addr)
	}
}

func TestIsGatewayReady_DownWhenNoPidAndNoHealth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultProxyConfig()
	cfg.Listen = "127.0.0.1:1" // closed port, and no PID file
	if err := WriteProxyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	RemovePidFile()
	if IsGatewayReady() {
		t.Errorf("IsGatewayReady = true, want false when nothing is listening")
	}
}

func TestIsStreaming(t *testing.T) {
	cases := []struct {
		contentType string
		want        bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"application/x-ndjson", true},
		{"application/json", false},
		{"text/plain", false},
	}
	for _, tc := range cases {
		resp := &http.Response{Header: http.Header{"Content-Type": []string{tc.contentType}}}
		got := isStreaming(resp)
		if got != tc.want {
			t.Errorf("isStreaming(%q) = %v, want %v", tc.contentType, got, tc.want)
		}
	}
}

func TestIsClaudeCodeRequest(t *testing.T) {
	r1, _ := http.NewRequest("POST", "/v1/responses", nil)
	if isClaudeCodeRequest(r1) {
		t.Error("Responses requests are not Claude Code Messages requests")
	}

	r2, _ := http.NewRequest("POST", "/v1/messages", nil)
	r2.Header.Set("x-api-key", "PROXY_MANAGED")
	if !isClaudeCodeRequest(r2) {
		t.Error("PROXY_MANAGED Messages request should detect Claude Code")
	}

	r2b, _ := http.NewRequest("POST", "/v1/messages", nil)
	r2b.Header.Set("x-api-key", "sk-ant-api03-aix-proxy-managed")
	r2b.Header.Set("User-Agent", "claude-cli/2.1.223")
	if !isClaudeCodeRequest(r2b) {
		t.Error("claude-cli User-Agent should detect Claude Code")
	}

	r2c, _ := http.NewRequest("POST", "/v1/messages", nil)
	r2c.Header.Set("x-api-key", "aix-gateway")
	if isClaudeCodeRequest(r2c) {
		t.Error("gateway-keyed Messages request should detect Desktop")
	}

	r2d, _ := http.NewRequest("POST", "/v1/messages", nil)
	r2d.Header.Set("User-Agent", "Mozilla/5.0 ... Claude/1.26832.0 ... Electron/38 ...")
	if isClaudeCodeRequest(r2d) {
		t.Error("Electron User-Agent should detect Desktop")
	}

	r2e, _ := http.NewRequest("POST", "/v1/messages", nil)
	if !isClaudeCodeRequest(r2e) {
		t.Error("unattributed Messages request should default to Claude Code")
	}
}

func TestInjectAuth_CustomHeaders(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"custom": {
			Name:      "custom",
			Upstream:  "https://api.example.com/v1",
			AuthToken: "sk-test123",
			Headers:   map[string]string{"X-Org": "myorg", "X-Region": "us"},
		},
	}}
	ps := NewProxyServer(cfg)
	p := cfg.Providers["custom"]

	req, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/chat/completions", nil)
	ps.injectAuth(req, p)

	if req.Header.Get("Authorization") != "Bearer sk-test123" {
		t.Errorf("Authorization = %q, want Bearer sk-test123", req.Header.Get("Authorization"))
	}
	if req.Header.Get("X-Org") != "myorg" {
		t.Errorf("X-Org = %q, want myorg", req.Header.Get("X-Org"))
	}
	if req.Header.Get("X-Region") != "us" {
		t.Errorf("X-Region = %q, want us", req.Header.Get("X-Region"))
	}
}

func TestInjectAuth_HeadersOverrideAuth(t *testing.T) {
	cfg := &ProxyConfig{Providers: map[string]*ProviderConfig{
		"custom": {
			Name:      "custom",
			Upstream:  "https://api.example.com/v1",
			AuthToken: "",
			Headers:   map[string]string{"Authorization": "ApiKey custom-key"},
		},
	}}
	ps := NewProxyServer(cfg)
	p := cfg.Providers["custom"]

	req, _ := http.NewRequest("POST", "http://127.0.0.1:2026/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer incoming-token")
	ps.injectAuth(req, p)

	if req.Header.Get("Authorization") != "ApiKey custom-key" {
		t.Errorf("Authorization = %q, want ApiKey custom-key (header override)", req.Header.Get("Authorization"))
	}
}

func TestApplyProviderHeaders_SkipsHost(t *testing.T) {
	p := &ProviderConfig{
		Headers: map[string]string{"Host": "evil.example.com", "X-Trace": "abc"},
	}
	req, _ := http.NewRequest("GET", "http://upstream.example.com/v1/models", nil)
	applyProviderHeaders(req, p)

	if req.Header.Get("Host") == "evil.example.com" {
		t.Error("Host header should not be overridable via Headers map")
	}
	if req.Header.Get("X-Trace") != "abc" {
		t.Errorf("X-Trace = %q, want abc", req.Header.Get("X-Trace"))
	}
}

func TestHandleModels_ModelNames(t *testing.T) {
	cfg := &ProxyConfig{
		GatewayKey: "test-key",
		Providers: map[string]*ProviderConfig{
			"custom": {
				Name:       "custom",
				Upstream:   "https://api.example.com/v1",
				Models:     map[string]string{"model-id": "model-id"},
				ModelNames: map[string]string{"model-id": "Display Name"},
			},
		},
	}
	ps := NewProxyServer(cfg)

	w := newMockResponseWriter()
	r, _ := http.NewRequest("GET", "/v1/models", nil)
	ps.handleModels(w, r, requestHarness(r.URL.Path), requestClient(r, requestHarness(r.URL.Path)))

	var resp struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("want 1 model, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != "model-id" {
		t.Errorf("id = %q, want model-id", resp.Data[0].ID)
	}
	if resp.Data[0].DisplayName != "Display Name" {
		t.Errorf("display_name = %q, want Display Name", resp.Data[0].DisplayName)
	}
}

func TestHandleModelsForProvider_ModelNamesFallback(t *testing.T) {
	cfg := &ProxyConfig{
		Providers: map[string]*ProviderConfig{
			"custom": {
				Name:     "custom",
				Upstream: "https://api.example.com/v1",
				Models:   map[string]string{"m1": "upstream-m1"},
			},
		},
	}
	ps := NewProxyServer(cfg)

	w := newMockResponseWriter()
	r, _ := http.NewRequest("GET", "/custom/v1/models", nil)
	ps.handleModelsForProvider(w, r, cfg.Providers["custom"], requestHarness(r.URL.Path), requestClient(r, requestHarness(r.URL.Path)))

	var resp struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("want 1 model, got %d", len(resp.Data))
	}
	if resp.Data[0].ID != "m1" {
		t.Errorf("id = %q, want m1", resp.Data[0].ID)
	}
	if resp.Data[0].DisplayName != "upstream-m1" {
		t.Errorf("display_name = %q, want upstream-m1 (fallback to map value)", resp.Data[0].DisplayName)
	}
}

func TestHandleRequest_CustomProviderHeadersForwarded(t *testing.T) {
	var gotAuth, gotOrg, gotModel string
	var mu sync.Mutex

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotOrg = r.Header.Get("X-Org")
		defer mu.Unlock()
		var req struct {
			Model string `json:"model"`
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		gotModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r1","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	cfg := &ProxyConfig{
		GatewayKey: "aix-gateway",
		Providers: map[string]*ProviderConfig{
			"custom": {
				Name:      "custom",
				Upstream:  upstream.URL,
				AuthToken: "sk-secret",
				Models:    map[string]string{"model-id": "model-id"},
				Headers:   map[string]string{"X-Org": "myorg"},
			},
		},
	}
	ps := NewProxyServer(cfg)

	body := []byte(`{"model":"model-id","messages":[{"role":"user","content":"hi"}]}`)
	r, _ := http.NewRequest("POST", "http://127.0.0.1:2026/custom/v1/chat/completions", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer aix-gateway")
	r.Header.Set("Content-Type", "application/json")

	w := newMockResponseWriter()
	ps.handleRequest(w, r)

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer sk-secret" {
		t.Errorf("upstream Authorization = %q, want Bearer sk-secret (gateway key replaced by provider token)", gotAuth)
	}
	if gotOrg != "myorg" {
		t.Errorf("upstream X-Org = %q, want myorg", gotOrg)
	}
	if gotModel != "model-id" {
		t.Errorf("upstream model = %q, want model-id", gotModel)
	}
}
