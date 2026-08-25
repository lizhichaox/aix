package internal

import (
	"bytes"
	"context"
	"encoding/json"
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

const maxProxyResponseBytes = 100 * 1024 * 1024

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

// ProxyServiceEnv starts the internal Claude gateway without exposing a
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
		client:    &http.Client{Timeout: 30 * time.Minute},
		startTime: time.Now(),
	}

	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err != nil {
			log.Printf("[aix-proxy] WARNING: invalid proxy URL %q: %v", cfg.Proxy, err)
		} else if t, ok := http.DefaultTransport.(*http.Transport); ok {
			transport := t.Clone()
			transport.Proxy = http.ProxyURL(proxyURL)
			ps.client.Transport = transport
			log.Printf("[aix-proxy] upstream connections routed through proxy: %s", proxyURL.String())
		} else {
			log.Printf("[aix-proxy] WARNING: DefaultTransport is not *http.Transport, proxy setting ignored")
		}
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
// API. AIX does not proxy the Responses API: Codex uses native providers
// (aix codex <provider>), which call the provider directly and bypass the
// proxy entirely.
func isResponsesPath(path string) bool {
	return strings.HasSuffix(path, "/v1/responses") || path == "/responses"
}

func isLocalAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "127.0.0.1" || host == "[::1]" || host == "localhost"
}

func (ps *ProxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("[aix-proxy] incoming: %s %s | x-api-key=%s Authorization=%s ua=%s",
		r.Method, r.URL.Path,
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
			ps.handleModelsForProvider(w, r, explicitProvider)
		} else {
			ps.handleModels(w, r)
		}
		return
	}

	// Read body once — used for model-based routing and downstream forwarding.
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 50*1024*1024))
	r.Body.Close()
	if err != nil {
		log.Printf("[aix-proxy] ERROR: read request body: %v", err)
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	bodyModel := extractModelFromBytes(bodyBytes)
	// Restore body so downstream handlers that read r.Body still work.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	if isResponsesPath(r.URL.Path) {
		// The Responses API is never proxied: Codex uses native providers
		// directly (aix codex <provider>), which bypass the proxy entirely.
		http.Error(w, `{"error":"Responses API is not proxied — Codex must use a native provider (aix codex <provider>)"}`, http.StatusNotImplemented)
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
	bodyBytes = ps.rewriteModel(bodyBytes, provider)

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		log.Printf("[aix-proxy] upstream error: %v", err)
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
		log.Printf("[aix-proxy] upstream error: %v", err)
		http.Error(w, `{"error":"upstream request failed"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Printf("[aix-proxy] upstream status: %d", resp.StatusCode)

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if isStreaming(resp) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			log.Printf("[aix-proxy] WARNING: streaming without http.Flusher for %s %s", r.Method, r.URL.Path)
		}
		var total int64
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					log.Printf("[aix-proxy] ERROR: streaming write failed after %d bytes: %v", total, werr)
					break
				}
				total += int64(n)
				if ok {
					flusher.Flush()
				}
			}
			if total >= maxProxyResponseBytes {
				log.Printf("[aix-proxy] WARNING: streaming response truncated at %d bytes (limit reached)", maxProxyResponseBytes)
				break
			}
			if err != nil {
				break
			}
		}
	} else {
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxProxyResponseBytes))
		if err != nil {
			log.Printf("[aix-proxy] read upstream body error: %v", err)
		}
		if _, werr := w.Write(respBody); werr != nil {
			log.Printf("[aix-proxy] write response error: %v", werr)
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

func (ps *ProxyServer) rewriteModel(body []byte, provider *ProviderConfig) []byte {
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
			if mapped, ok := provider.Models[model]; ok {
				data["model"], _ = json.Marshal(mapped)
				log.Printf("[aix-proxy] model: %s → %s", model, mapped)
			} else if base := strings.TrimSuffix(model, "[1m]"); base != model {
				// Claude Desktop and Claude Code address 1M-context variants
				// as "<alias>[1m]"; the suffix is a client-side context hint,
				// and AIX upstreams that support 1M context serve it under the
				// base model id, so rewrite the variant to the same mapping.
				if mapped, ok := provider.Models[base]; ok {
					data["model"], _ = json.Marshal(mapped)
					log.Printf("[aix-proxy] model: %s → %s", model, mapped)
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

func (ps *ProxyServer) handleModels(w http.ResponseWriter, r *http.Request) {
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
	log.Printf("[aix-proxy] /v1/models → %d models", len(models))
}

func (ps *ProxyServer) handleModelsForProvider(w http.ResponseWriter, r *http.Request, provider *ProviderConfig) {
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
	log.Printf("[aix-proxy] /v1/models (%s) → %d models", provider.Name, len(models))
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
