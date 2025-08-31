package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// WebSearcher is an interface for web search providers.
type WebSearcher interface {
	Search(ctx context.Context, query string) ([]string, error)
}

// WebSearchProvider is a registry for web search providers by name.
var webSearchProviders = map[string]WebSearcher{}

// RegisterWebSearcher registers a web search provider by name.
func RegisterWebSearcher(name string, ws WebSearcher) {
	webSearchProviders[name] = ws
}

// SearchWeb performs a web search using the specified provider.
// If providerName is empty or not found, it falls back to the mock provider.
func SearchWeb(ctx context.Context, providerName, query string) ([]string, error) {
	if providerName == "" {
		providerName = "mock"
	}
	if ws, ok := webSearchProviders[providerName]; ok {
		return ws.Search(ctx, query)
	}
	return (&MockWebSearcher{}).Search(ctx, query)
}

// MockWebSearcher is a fallback web search provider for testing.
type MockWebSearcher struct{}

func (m *MockWebSearcher) Search(ctx context.Context, query string) ([]string, error) {
	return []string{"This is a mock search result for: " + query}, nil
}

// DuckDuckGoWebSearcher implements WebSearcher using DuckDuckGo's Instant Answer API.
type DuckDuckGoWebSearcher struct{}

func (d *DuckDuckGoWebSearcher) Search(ctx context.Context, query string) ([]string, error) {
	// Use DuckDuckGo's Instant Answer API (no API key required)
	endpoint := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) + "&format=json&no_redirect=1&no_html=1"
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, errors.New("duckduckgo: bad status " + resp.Status + " body: " + string(data))
	}
	var result struct {
		RelatedTopics []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
		AbstractText string `json:"AbstractText"`
		AbstractURL  string `json:"AbstractURL"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&result); err != nil {
		return nil, err
	}
	var out []string
	if result.AbstractText != "" {
		out = append(out, result.AbstractText+" ("+result.AbstractURL+")")
	}
	for _, t := range result.RelatedTopics {
		if t.Text != "" && t.FirstURL != "" {
			out = append(out, t.Text+" ("+t.FirstURL+")")
		}
	}
	if len(out) == 0 {
		out = append(out, "No results found.")
	}
	return out, nil
}

type StreamHandler func(chunk string)

// Provider is an abstraction over different AI providers.
// Implementations should call the handler for each chunk they receive
// and return nil on normal completion or an error on failure.
type Provider interface {
	Stream(ctx context.Context, prompt string, handler StreamHandler) error
}

var providers = map[string]Provider{}

// Register makes a provider available by name.
func Register(name string, p Provider) {
	providers[name] = p
}

// Stream looks up a provider by name and streams the response using the handler.
// If provider is not found it falls back to a built-in mock provider.
func Stream(ctx context.Context, providerName string, prompt string, handler StreamHandler) error {
	if providerName == "" {
		providerName = "mock"
	}
	if p, ok := providers[providerName]; ok {
		return p.Stream(ctx, prompt, handler)
	}
	// fallback
	return (&MockProvider{}).Stream(ctx, prompt, handler)
}

// MockProvider returns simulated chunks useful for local testing.
type MockProvider struct{}

func (m *MockProvider) Stream(ctx context.Context, prompt string, handler StreamHandler) error {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("empty prompt")
	}
	// simple chunking by words
	words := strings.Fields(prompt)
	if len(words) < 6 {
		chunks := []string{"Hello,", "this is a mock AI reply.", "Replace with a real provider."}
		for _, c := range chunks {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				handler(c)
				time.Sleep(250 * time.Millisecond)
			}
		}
		return nil
	}

	// emit slices of the prompt
	chunkSize := 6
	for i := 0; i < len(words); i += chunkSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		end := i + chunkSize
		if end > len(words) {
			end = len(words)
		}
		handler(strings.Join(words[i:end], " "))
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// HTTPProvider is a simple, configurable provider that POSTs the prompt to an HTTP endpoint.
// It supports both full-response and chunked streaming responses (line-delimited).
type HTTPProvider struct {
	Endpoint      string
	ApiKeyEnv     string // environment variable name that holds the API key (optional)
	Model         string
	StreamEnabled bool
	// optional extra headers can be added later
}

// NewHTTPProvider creates a configured HTTPProvider instance.
func NewHTTPProvider(endpoint, apiKeyEnv, model string, streamEnabled bool) *HTTPProvider {
	return &HTTPProvider{Endpoint: endpoint, ApiKeyEnv: apiKeyEnv, Model: model, StreamEnabled: streamEnabled}
}

func (h *HTTPProvider) Stream(ctx context.Context, prompt string, handler StreamHandler) error {
	if strings.TrimSpace(h.Endpoint) == "" {
		return errors.New("http provider: endpoint is empty")
	}

	// build request body generically
	body := map[string]any{"prompt": prompt}
	if h.Model != "" {
		body["model"] = h.Model
	}
	if h.StreamEnabled {
		body["stream"] = true
	}

	b, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", h.Endpoint, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.ApiKeyEnv != "" {
		if k := os.Getenv(h.ApiKeyEnv); k != "" {
			req.Header.Set("Authorization", "Bearer "+k)
		}
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// attempt to read body for error details
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return errors.New("http provider: bad status " + resp.Status + " body: " + string(data))
	}

	if !h.StreamEnabled {
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		handler(string(data))
		return nil
	}

	// stream: read line-delimited/chunked body and call handler for each non-empty line
	reader := bufio.NewReader(resp.Body)
	isOllama := strings.Contains(strings.ToLower(h.Endpoint), "ollama") || strings.Contains(strings.ToLower(h.Endpoint), "11434")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			log.Printf("http provider: stream read error: %v", err)
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isOllama {
			// Try to parse as JSON and extract 'response' field
			var chunk struct {
				Response string `json:"response"`
			}
			if err := json.Unmarshal([]byte(line), &chunk); err == nil && chunk.Response != "" {
				handler(chunk.Response)
			}
			// else ignore or log parse errors
		} else {
			handler(line)
		}
	}
}

func init() {
	// register builtin mock provider
	Register("mock", &MockProvider{})

	// Register Ollama provider using environment variables
	ollamaEndpoint := os.Getenv("OLLAMA_ENDPOINT")
	if ollamaEndpoint == "" {
		ollamaEndpoint = "http://localhost:11434/api/generate"
	}
	ollamaModel := os.Getenv("OLLAMA_MODEL")
	if ollamaModel == "" {
		ollamaModel = "llama3"
	}
	ollamaApiKeyEnv := "OLLAMA_API_KEY"
	ollama := NewHTTPProvider(ollamaEndpoint, ollamaApiKeyEnv, ollamaModel, true)
	Register("ollama", ollama)

	// register DuckDuckGo web search provider
	RegisterWebSearcher("duckduckgo", &DuckDuckGoWebSearcher{})
	RegisterWebSearcher("mock", &MockWebSearcher{})
}
