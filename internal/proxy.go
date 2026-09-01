package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	maxProxyRequestBytes          = 50 * 1024 * 1024
	upstreamResponseHeaderTimeout = 5 * time.Minute
)

// DefaultGatewayAPIKey is the local credential shared by AIX-managed clients
// and the AIX proxy. It is not an upstream provider credential.
const DefaultGatewayAPIKey = "aix-claude-gateway-api-key"

type modelEntry struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
}

type modelsResponse struct {
	Data    []modelEntry `json:"data"`
	HasMore bool         `json:"has_more"`
}

var ProxyVersion = "dev"

// ProxyServiceEnv starts the internal AIX gateway without exposing a
// user-facing proxy command. Service managers and the on-demand launcher set
// this variable before executing aix.
const ProxyServiceEnv = "AIX_INTERNAL_PROXY"

// ServiceInstallEnv refreshes the private service definition after a binary
// install without adding a public lifecycle command.
const ServiceInstallEnv = "AIX_INTERNAL_INSTALL_SERVICE"

type ProxyConfig struct {
	Listen     string                     `toml:"listen"`
	GatewayKey string                     `toml:"gateway_key"`
	Proxy      string                     `toml:"proxy"`
	Providers  map[string]*ProviderConfig `toml:"providers"`
}

type ProviderConfig struct {
	Name            string            `toml:"name"`
	Upstream        string            `toml:"upstream"`
	AuthToken       string            `toml:"auth_token"`
	Models          map[string]string `toml:"models"`
	ModelNames      map[string]string `toml:"model_names,omitempty"`
	Headers         map[string]string `toml:"headers,omitempty"`
	AnthropicNative bool              `toml:"anthropic,omitempty"`
}

// providerIsAnthropic reports whether a proxy provider exposes an
// Anthropic-compatible Messages API upstream. Upstreams are detected by the
// legacy "/anthropic" path convention or by the explicit anthropic flag used
// for gateways like OpenRouter and OpenCode whose base URL carries no such
// path.
func providerIsAnthropic(p *ProviderConfig) bool {
	return p != nil && (p.AnthropicNative || strings.Contains(p.Upstream, "/anthropic"))
}

func DefaultProxyConfig() *ProxyConfig {
	return &ProxyConfig{
		Listen:     "127.0.0.1:2026",
		GatewayKey: DefaultGatewayAPIKey,
	}
}

func LoadProxyConfig() (*ProxyConfig, error) {
	cfg := DefaultProxyConfig()
	_, err := toml.DecodeFile(ProxyConfigPath(), cfg)
	return cfg, err
}

type ProxyHealth struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
	Listen  string `json:"listen"`
}

func FetchProxyHealth(listenAddr string) (*ProxyHealth, error) {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + listenAddr + "/health")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var h ProxyHealth
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return nil, err
	}
	return &h, nil
}

func IsProxyRunning() (bool, int) {
	pid, err := ReadPidFile()
	if err != nil {
		return false, 0
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return false, 0
	}
	return true, pid
}

// IsGatewayReady reports whether the AIX gateway process can serve requests.
// It trusts the health endpoint rather than only the PID file, which can be
// stale or temporarily unreadable; a healthy listener is the authoritative
// signal that managed harnesses can be served.
func IsGatewayReady() bool {
	if running, _ := IsProxyRunning(); running {
		return true
	}
	cfg, err := LoadProxyConfig()
	if err != nil {
		return false
	}
	health, err := FetchProxyHealth(cfg.Listen)
	return err == nil && health != nil && health.Status == "ok"
}

func MaskHeader(v string) string {
	if v == "" {
		return ""
	}
	if len(v) < 3 {
		return "***"
	}
	if len(v) <= 10 {
		return v[:3] + "***"
	}
	return v[:3] + "***" + v[len(v)-4:]
}

type ProxyServer struct {
	config    *ProxyConfig
	server    *http.Server
	client    *http.Client
	startTime time.Time
}

func NewProxyServer(cfg *ProxyConfig) *ProxyServer {
	ps := &ProxyServer{
		config:    cfg,
		client:    &http.Client{},
		startTime: time.Now(),
	}

	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		transport := t.Clone()
		transport.ResponseHeaderTimeout = upstreamResponseHeaderTimeout
		if cfg.Proxy != "" {
			proxyURL, err := url.Parse(cfg.Proxy)
			if err != nil {
				log.Printf("[aix-proxy] WARNING: invalid proxy URL %q: %v", cfg.Proxy, err)
			} else {
				transport.Proxy = http.ProxyURL(proxyURL)
				log.Printf("[aix-proxy] upstream connections routed through proxy: %s", proxyURL.String())
			}
		}
		ps.client.Transport = transport
	} else {
		log.Printf("[aix-proxy] WARNING: DefaultTransport is not *http.Transport; response-header timeout and proxy settings are unavailable")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", ps.handleHealth)
	mux.HandleFunc("/", ps.handleRequest)
	ps.server = &http.Server{
		Addr:    cfg.Listen,
		Handler: mux,
	}
	return ps
}

func (ps *ProxyServer) Start() error {
	log.Printf("[aix-proxy] listening on %s", ps.config.Listen)
	for name, p := range ps.config.Providers {
		log.Printf("[aix-proxy] provider '%s' → %s", name, p.Upstream)
	}

	ch := make(chan error, 1)
	go func() { ch <- ps.server.ListenAndServe() }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-ch:
		return err
	case sig := <-sigCh:
		log.Printf("[aix-proxy] received %v, shutting down gracefully (waiting up to 30s)...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ps.server.Shutdown(ctx)
		return nil
	}
}

func (ps *ProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"version": ProxyVersion,
		"uptime":  time.Since(ps.startTime).Truncate(time.Millisecond).String(),
		"listen":  ps.config.Listen,
	})
}

// isClaudeCodeRequest distinguishes local Claude Code traffic from Claude
// Desktop traffic for the localhost authentication bypass. Claude Code,
// whether run from a terminal or inside Desktop's Code tab, identifies itself
// as claude-cli/<version>. Requests without a Desktop signal keep the legacy
// Claude Code behavior.
func isClaudeCodeRequest(r *http.Request) bool {
	if !strings.Contains(r.URL.Path, "/v1/messages") {
		return false
	}
	ua := r.Header.Get("User-Agent")
	apiKey := strings.TrimSpace(r.Header.Get("x-api-key"))
	switch {
	case strings.HasPrefix(strings.ToLower(ua), "claude-cli/"):
		// Claude Code CLI (terminal or Desktop-embedded; both use the same
		// binary and UA). Checked first so an env-keyed CLI request can never
		// be misclassified and lose the localhost bypass.
		return true
	case apiKey != "" && apiKey != "PROXY_MANAGED" && !strings.HasPrefix(apiKey, "sk-ant-"):
		// Gateway-authenticated Desktop client (gateway key from config library).
		return false
	case strings.Contains(ua, "Electron") || strings.Contains(ua, "Claude/"):
		// Claude Desktop's own HTTP stack (no gateway key header).
		return false
	}
	return true
}

// isResponsesPath reports whether the request targets the OpenAI Responses
// API. AIX passes this protocol through unchanged for Codex routes.
func isResponsesPath(path string) bool {
	for _, marker := range []string{"/v1/responses", "/responses"} {
		if idx := strings.Index(path, marker); idx >= 0 {
			rest := path[idx+len(marker):]
			if rest == "" || strings.HasPrefix(rest, "/") {
				return true
			}
		}
	}
	return false
}

// requestHarness identifies a public harness only when the request protocol
// makes the relationship unambiguous. Claude Code and Claude Desktop are one
// public Claude harness and both use the Anthropic Messages paths. Responses
// paths belong to the public Codex harness.
func requestHarness(path string) string {
	if strings.Contains(path, "/v1/messages") {
		return HarnessClaude
	}
	if isResponsesPath(path) || hasRoutePrefix(path, "codex-") {
		return HarnessCodex
	}
	return "unknown"
}

func hasRoutePrefix(path, prefix string) bool {
	first := strings.TrimPrefix(path, "/")
	if slash := strings.IndexByte(first, '/'); slash >= 0 {
		first = first[:slash]
	}
	return strings.HasPrefix(first, prefix)
}

// requestClient reports only identities that the HTTP request itself can
// support. It deliberately avoids per-request process inspection, which is
// platform-specific and races with short-lived localhost connections.
func requestClient(r *http.Request, harness string) string {
	ua := strings.ToLower(strings.TrimSpace(r.UserAgent()))
	switch harness {
	case HarnessClaude:
		if isClaudeCodeRequest(r) {
			return "claude-code"
		}
		return "claude-desktop"
	case HarnessCodex:
		if strings.Contains(ua, "codex desktop") || strings.Contains(ua, "electron") || strings.Contains(ua, "chatgpt") {
			return "codex-desktop"
		}
		if strings.Contains(ua, "codex") {
			return "codex-cli"
		}
		return "codex-client"
	default:
		switch {
		case strings.HasPrefix(ua, "curl/"):
			return "curl"
		case strings.HasPrefix(ua, "go-http-client/"):
			return "go-http-client"
		default:
			return "unknown-client"
		}
	}
}

func isLocalAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "[::1]" || host == "localhost"
}

func (ps *ProxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	harness := requestHarness(r.URL.Path)
	client := requestClient(r, harness)
	log.Printf("[%s] [aix-proxy] incoming: client=%s method=%s path=%s | x-api-key=%s Authorization=%s ua=%s",
		harness, client, r.Method, r.URL.Path,
		MaskHeader(r.Header.Get("x-api-key")),
		MaskHeader(r.Header.Get("Authorization")),
		r.UserAgent())

	if r.URL.Path == "/" && (r.Method == "HEAD" || r.Method == "GET") {
		w.WriteHeader(http.StatusOK)
		return
	}

	if ps.config.GatewayKey != "" {
		apiKey := r.Header.Get("x-api-key")
		authHeader := r.Header.Get("Authorization")
		bearerToken := strings.TrimPrefix(authHeader, "Bearer ")

		accepted := apiKey == ps.config.GatewayKey || bearerToken == ps.config.GatewayKey

		// PROXY_MANAGED is only accepted when a provider explicitly
		// uses it as its auth_token (Claude Code passthrough flow).
		// Otherwise the magic value stays a no-op so that custom
		// gateway_key deployments are not left with a backdoor.
		if !accepted && (apiKey == "PROXY_MANAGED" || bearerToken == "PROXY_MANAGED") {
			for _, p := range ps.config.Providers {
				if p.AuthToken == "PROXY_MANAGED" {
					accepted = true
					break
				}
			}
		}

		if !accepted {
			for _, p := range ps.config.Providers {
				if p.AuthToken != "" && (apiKey == p.AuthToken || bearerToken == p.AuthToken) {
					accepted = true
					break
				}
			}
		}

		if !accepted && isClaudeCodeRequest(r) && isLocalAddr(r.RemoteAddr) {
			accepted = true
		}

		if !accepted {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
	} else {
		// Without a gateway_key, reject requests from unrecognized clients.
		if !strings.Contains(r.URL.Path, "/v1/responses") &&
			r.Header.Get("x-api-key") != "PROXY_MANAGED" &&
			r.Header.Get("x-api-key") == "" &&
			r.Header.Get("Authorization") == "" {
			http.Error(w, `{"error":"unrecognized client — set a gateway_key or use a supported client"}`, http.StatusUnauthorized)
			return
		}
	}

	// Route-based provider dispatch: /<provider>/v1/... selects provider explicitly.
	providerName, strippedPath := ps.extractProviderPrefix(r.URL.Path)
	var explicitProvider *ProviderConfig
	if providerName != "" {
		explicitProvider = ps.config.Providers[providerName]
		r.URL.Path = strippedPath
	}
	if r.URL.Path == "/v1/models" || strings.HasSuffix(r.URL.Path, "/v1/models") {
		if explicitProvider != nil {
			ps.handleModelsForProvider(w, r, explicitProvider, harness, client)
		} else {
			ps.handleModels(w, r, harness, client)
		}
		return
	}

	// Read body once — used for model-based routing and downstream forwarding.
	bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxProxyRequestBytes))
	r.Body.Close()
	if err != nil {
		log.Printf("[%s] [aix-proxy] ERROR: read request body: %v", harness, err)
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, `{"error":"request body too large"}`, http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	bodyModel := extractModelFromBytes(bodyBytes)
	// Restore body so downstream handlers that read r.Body still work.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	if isResponsesPath(r.URL.Path) {
		if explicitProvider == nil || !strings.HasPrefix(providerName, "codex-") {
			http.Error(w, `{"error":"Responses requests require an explicit codex-<provider> AIX route"}`, http.StatusBadRequest)
			return
		}
	}
	if strings.Contains(r.URL.Path, "/v1/messages") && explicitProvider != nil && !providerIsAnthropic(explicitProvider) {
		http.Error(w, `{"error":"Messages requests require an Anthropic-compatible AIX provider route"}`, http.StatusBadRequest)
		return
	}

	provider := explicitProvider
	if provider == nil {
		provider = ps.detectProvider(r, bodyModel)
	}
	if provider == nil {
		http.Error(w, `{"error":"no provider configured for this route"}`, http.StatusBadGateway)
		return
	}

	upstreamURL := singleJoinSlash(provider.Upstream, r.URL.Path)
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	// Rewrite model in the pre-read body bytes.
	bodyBytes = ps.rewriteModel(bodyBytes, provider, harness)
	// Pin the reasoning effort to the configured value for Codex Responses
	// routes. The desktop client sends its own per-request effort (commonly
	// defaulting to medium), which can drift from the configured
	// model_reasoning_effort and the catalog's declared default. Normalizing it
	// keeps `aix status` and actual request usage consistent.
	if requestHarness(r.URL.Path) == HarnessCodex {
		if effort := CurrentHarnessEffort(HarnessCodex); effort != "" {
			bodyBytes = setReasoningEffort(bodyBytes, effort)
		}
	}
	routedModel := extractModelFromBytes(bodyBytes)
	routedEffort := extractEffortFromBytes(bodyBytes)
	publicProvider := publicProxyProviderID(providerName)
	if publicProvider == "" {
		publicProvider = provider.Name
	}
	harness = requestHarness(r.URL.Path)
	log.Printf("[%s] [aix-proxy] route: client=%s provider=%s model=%s effort=%s method=%s path=%s",
		harness, client, logValue(publicProvider), logValue(routedModel), logValue(routedEffort), r.Method, r.URL.Path)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		log.Printf("[%s] [aix-proxy] upstream error: %v", harness, err)
		http.Error(w, `{"error":"failed to create upstream request"}`, http.StatusInternalServerError)
		return
	}

	for k, vs := range r.Header {
		if k == "Host" || k == "X-Forwarded-For" {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	ps.injectAuth(req, provider)

	resp, err := ps.client.Do(req)
	if err != nil {
		log.Printf("[%s] [aix-proxy] upstream error: %v", harness, err)
		http.Error(w, `{"error":"upstream request failed"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Printf("[%s] [aix-proxy] response: client=%s provider=%s model=%s effort=%s status=%d",
		harness, client, logValue(publicProvider), logValue(routedModel), logValue(routedEffort), resp.StatusCode)

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if isStreaming(resp) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			log.Printf("[%s] [aix-proxy] WARNING: streaming without http.Flusher for %s %s", harness, r.Method, r.URL.Path)
		}
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					log.Printf("[%s] [aix-proxy] ERROR: streaming write failed: %v", harness, werr)
					break
				}
				if ok {
					flusher.Flush()
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[%s] [aix-proxy] ERROR: streaming read failed: %v", harness, err)
				}
				break
			}
		}
	} else {
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("[%s] [aix-proxy] copy upstream response error: %v", harness, err)
		}
	}
}

func (ps *ProxyServer) detectProvider(r *http.Request, modelHint string) *ProviderConfig {
	path := r.URL.Path
	providerNames := ps.providerNamesSorted()

	// Messages API: find Anthropic-compatible provider
	if strings.HasSuffix(path, "/v1/messages") {
		for _, name := range providerNames {
			p := ps.config.Providers[name]
			if providerIsAnthropic(p) {
				return p
			}
		}
		// Fallback: return first available provider
		if len(providerNames) == 0 {
			return nil
		}
		return ps.config.Providers[providerNames[0]]
	}

	// Chat Completions / Models: match by model name
	// (Responses API is handled earlier; /v1/models is handled via handleModels.)
	if strings.HasSuffix(path, "/v1/chat/completions") {

		model := modelHint
		if model == "" {
			model = extractModelFromBody(r)
		}
		// Match model against providers' models map values (upstream names).
		// Two-pass: prefer non-anthropic providers to avoid routing
		// non-Messages API requests to /anthropic endpoints when the
		// same upstream model appears in multiple providers.
		for _, name := range providerNames {
			p := ps.config.Providers[name]
			if providerIsAnthropic(p) {
				continue
			}
			for _, mappedModel := range p.Models {
				if mappedModel == model {
					return p
				}
			}
		}
		for _, name := range providerNames {
			p := ps.config.Providers[name]
			for _, mappedModel := range p.Models {
				if mappedModel == model {
					return p
				}
			}
		}
		// Match model against providers' models map keys (source names).
		for _, name := range providerNames {
			p := ps.config.Providers[name]
			if providerIsAnthropic(p) {
				continue
			}
			for srcModel := range p.Models {
				if srcModel == model {
					return p
				}
			}
		}
		for _, name := range providerNames {
			p := ps.config.Providers[name]
			for srcModel := range p.Models {
				if srcModel == model {
					return p
				}
			}
		}
		// Try to match model against provider names
		if model != "" {
			for _, name := range providerNames {
				p := ps.config.Providers[name]
				if strings.EqualFold(name, model) || (p.Name != "" && strings.EqualFold(p.Name, model)) {
					return p
				}
			}
		}
		// Fallback: first non-anthropic provider
		for _, name := range providerNames {
			p := ps.config.Providers[name]
			if !providerIsAnthropic(p) {
				return p
			}
		}
		// Fallback: return first available provider
		if len(providerNames) == 0 {
			return nil
		}
		return ps.config.Providers[providerNames[0]]
	}

	// Default route
	if p, ok := ps.config.Providers["default"]; ok {
		return p
	}
	if len(ps.config.Providers) == 1 {
		// No anthropic provider configured; return first available provider as fallback.
		if len(providerNames) == 0 {
			return nil
		}
		return ps.config.Providers[providerNames[0]]
	}
	return nil
}

// providerNamesSorted returns provider names in deterministic order.
func (ps *ProxyServer) providerNamesSorted() []string {
	names := make([]string, 0, len(ps.config.Providers))
	for name := range ps.config.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// extractModelFromBytes extracts the "model" field from JSON bytes.
func extractModelFromBytes(data []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(data, &req) == nil {
		return req.Model
	}
	return ""
}

func extractEffortFromBytes(data []byte) string {
	var req struct {
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if json.Unmarshal(data, &req) == nil {
		return req.Reasoning.Effort
	}
	return ""
}

// setReasoningEffort pins the Responses reasoning effort to a fixed value.
// The Codex desktop sends its own per-request effort, which can drift from the
// configured model_reasoning_effort and the catalog's declared
// default_reasoning_level; normalizing keeps status and actual usage
// consistent. An empty effort leaves the body untouched.
func setReasoningEffort(body []byte, effort string) []byte {
	if effort == "" || len(body) == 0 {
		return body
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}
	var reasoning map[string]json.RawMessage
	if raw, ok := data["reasoning"]; ok {
		_ = json.Unmarshal(raw, &reasoning)
	}
	if reasoning == nil {
		reasoning = make(map[string]json.RawMessage)
	}
	reasoning["effort"], _ = json.Marshal(effort)
	data["reasoning"], _ = json.Marshal(reasoning)
	rewritten, err := json.Marshal(data)
	if err != nil {
		return body
	}
	return rewritten
}

func publicProxyProviderID(providerID string) string {
	providerID = strings.TrimPrefix(providerID, "codex-")
	if providerID == DeepSeekAnthropicProviderID {
		return "deepseek"
	}
	return providerID
}

func logValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

// extractModelFromBody reads and restores request body, returning the model field value.
func extractModelFromBody(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	// Read at most 1MB for model extraction; restore the full body
	// for downstream handlers via MultiReader (pre-read bytes + remainder).
	data, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(data), r.Body))
	if err != nil {
		return ""
	}
	return extractModelFromBytes(data)
}

func (ps *ProxyServer) injectAuth(req *http.Request, provider *ProviderConfig) {
	req.Header.Del("x-api-key")
	req.Header.Del("Authorization")
	if provider.AuthToken != "" {
		if strings.Contains(req.URL.Path, "/v1/messages") {
			req.Header.Set("x-api-key", provider.AuthToken)
		} else {
			req.Header.Set("Authorization", "Bearer "+provider.AuthToken)
		}
	}
	applyProviderHeaders(req, provider)
}

// applyProviderHeaders sets per-provider custom headers on the upstream
// request. Applied after standard auth so they can override it (e.g. when
// auth is managed entirely via headers and auth_token is left empty).
func applyProviderHeaders(req *http.Request, provider *ProviderConfig) {
	for k, v := range provider.Headers {
		if strings.EqualFold(k, "Host") {
			continue
		}
		req.Header.Set(k, v)
	}
}

func (ps *ProxyServer) rewriteModel(body []byte, provider *ProviderConfig, harness string) []byte {
	if len(provider.Models) == 0 || len(body) == 0 {
		return body
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}

	if raw, ok := data["model"]; ok {
		var model string
		if json.Unmarshal(raw, &model) == nil {
			mapped, ok := provider.Models[model]
			if !ok {
				base := strings.TrimSuffix(model, "[1m]")
				// Claude Desktop and Claude Code address 1M-context variants
				// as "<alias>[1m]"; the suffix is a client-side context hint,
				// and AIX upstreams that support 1M context serve it under the
				// base model id, so rewrite the variant to the same mapping.
				if base != model {
					mapped, ok = provider.Models[base]
				}
			}
			if ok {
				data["model"], _ = json.Marshal(mapped)
				// Log only real rewrites. Identity mappings (e.g. live-catalog
				// providers whose client model already matches upstream) are
				// pure noise.
				if model != mapped {
					log.Printf("[%s] [aix-proxy] model: %s → %s", harness, model, mapped)
				}
			}
		}
	}

	rewritten, err := json.Marshal(data)
	if err != nil {
		return body
	}
	return rewritten
}

func isStreaming(resp *http.Response) bool {
	ct := resp.Header.Get("Content-Type")
	return strings.Contains(ct, "text/event-stream") ||
		strings.Contains(ct, "application/x-ndjson")
}

func (ps *ProxyServer) handleModels(w http.ResponseWriter, r *http.Request, harness, client string) {
	seen := make(map[string]bool)
	var models []modelEntry

	for _, name := range ps.providerNamesSorted() {
		provider := ps.config.Providers[name]
		srcModels := make([]string, 0, len(provider.Models))
		for srcModel := range provider.Models {
			srcModels = append(srcModels, srcModel)
		}
		sort.Strings(srcModels)
		for _, srcModel := range srcModels {
			if !seen[srcModel] {
				seen[srcModel] = true
				models = append(models, modelEntry{
					ID:          srcModel,
					Type:        "model",
					DisplayName: providerDisplayNameFor(provider, srcModel),
				})
			}
		}
	}

	resp := modelsResponse{Data: models, HasMore: false}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
	log.Printf("[%s] [aix-proxy] /v1/models: client=%s models=%d", harness, client, len(models))
}

func (ps *ProxyServer) handleModelsForProvider(w http.ResponseWriter, r *http.Request, provider *ProviderConfig, harness, client string) {
	var models []modelEntry
	srcModels := make([]string, 0, len(provider.Models))
	for srcModel := range provider.Models {
		srcModels = append(srcModels, srcModel)
	}
	sort.Strings(srcModels)
	for _, srcModel := range srcModels {
		models = append(models, modelEntry{
			ID:          srcModel,
			Type:        "model",
			DisplayName: providerDisplayNameFor(provider, srcModel),
		})
	}

	resp := modelsResponse{Data: models, HasMore: false}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
	log.Printf("[%s] [aix-proxy] /v1/models: client=%s provider=%s models=%d", harness, client, provider.Name, len(models))
}

// providerDisplayNameFor returns the display name for a source model: the
// explicit ModelNames entry if present, otherwise the mapped upstream model.
func providerDisplayNameFor(provider *ProviderConfig, srcModel string) string {
	if provider.ModelNames != nil {
		if dn, ok := provider.ModelNames[srcModel]; ok && dn != "" {
			return dn
		}
	}
	return provider.Models[srcModel]
}

// extractProviderPrefix checks whether the path starts with /<provider>/ where
// provider is a configured provider name. If so, it returns the provider name
// and the path with the prefix removed; otherwise it returns ("", path).
func (ps *ProxyServer) extractProviderPrefix(path string) (string, string) {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
	if len(parts) < 2 {
		return "", path
	}
	name := parts[0]
	if _, ok := ps.config.Providers[name]; !ok {
		return "", path
	}
	stripped := "/" + parts[1]
	return name, stripped
}

func singleJoinSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	if aslash && bslash {
		return a + b[1:]
	}
	if !aslash && !bslash {
		return a + "/" + b
	}
	return a + b
}

func ProxyConfigPath() string {
	return filepath.Join(AixDir(), "proxy.toml")
}

func WritePidFile(pid int) error {
	return os.WriteFile(PidFilePath(), []byte(strconv.Itoa(pid)), 0600)
}

func ReadPidFile() (int, error) {
	data, err := os.ReadFile(PidFilePath())
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

func RemovePidFile() {
	os.Remove(PidFilePath())
}

func PidFilePath() string {
	return filepath.Join(AixDir(), "proxy.pid")
}

func ProxyLogPath() string {
	return filepath.Join(AixDir(), "proxy.log")
}

// ValidateAPIKey tests whether an API key is valid for a given upstream URL.
// Returns true if the key is accepted (200), false if rejected (401/403).
// Other status codes are treated as inconclusive and return (true, error).
func ValidateAPIKey(upstream, authToken string, timeout time.Duration) (bool, string) {
	base := strings.TrimSuffix(strings.TrimSuffix(upstream, "/"), "/v1")
	modelsURL := base + "/v1/models"

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		return false, fmt.Sprintf("bad URL: %v", err)
	}
	if strings.Contains(upstream, "/anthropic") {
		req.Header.Set("x-api-key", authToken)
	} else {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return true, "API key valid (200)"
	case http.StatusUnauthorized:
		return false, "API key rejected (401)"
	case http.StatusForbidden:
		return false, "API key forbidden (403)"
	default:
		return true, fmt.Sprintf("status %d (endpoint may differ)", resp.StatusCode)
	}
}
