package config

// ProviderInfo describes a supported backend. Default model ids were
// checked against each provider's docs on 2026-10-19; every one of them can
// be overridden with `git quill config set model <id>`.
type ProviderInfo struct {
	Name           string
	Title          string
	DefaultModel   string // "" means the user must choose one
	KeyEnv         []string
	NeedsKey       bool
	DefaultBaseURL string
	DefaultBudget  int // max input tokens per request
	Local          bool
	// Kind selects the client implementation.
	Kind string // anthropic | openai | gemini | ollama | fake
}

// Providers is the registry, in display order.
var Providers = []ProviderInfo{
	{Name: "anthropic", Title: "Anthropic (Claude)", DefaultModel: "claude-haiku-5-5", KeyEnv: []string{"ANTHROPIC_API_KEY"},
		NeedsKey: true, DefaultBudget: 60000, Kind: "anthropic"},
	{Name: "openai", Title: "OpenAI", DefaultModel: "gpt-6.1-sol", KeyEnv: []string{"OPENAI_API_KEY"},
		NeedsKey: true, DefaultBaseURL: "https://api.openai.com/v1", DefaultBudget: 60000, Kind: "openai"},
	{Name: "gemini", Title: "Google Gemini", DefaultModel: "gemini-3.8-flash", KeyEnv: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		NeedsKey: true, DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta", DefaultBudget: 60000, Kind: "gemini"},
	{Name: "ollama", Title: "Ollama (local, free)", DefaultModel: "qwen3.5:4b",
		DefaultBudget: 6000, Local: true, Kind: "ollama"},
	{Name: "openrouter", Title: "OpenRouter (OpenAI-compatible)", KeyEnv: []string{"OPENROUTER_API_KEY"},
		NeedsKey: true, DefaultBaseURL: "https://openrouter.ai/api/v1", DefaultBudget: 30000, Kind: "openai"},
	{Name: "groq", Title: "Groq (OpenAI-compatible)", KeyEnv: []string{"GROQ_API_KEY"},
		NeedsKey: true, DefaultBaseURL: "https://api.groq.com/openai/v1", DefaultBudget: 20000, Kind: "openai"},
	{Name: "lmstudio", Title: "LM Studio (local, OpenAI-compatible)",
		DefaultBaseURL: "http://localhost:1234/v1", DefaultBudget: 6000, Local: true, Kind: "openai"},
	{Name: "compatible", Title: "Any OpenAI-compatible endpoint", KeyEnv: []string{"OPENAI_COMPATIBLE_API_KEY"},
		DefaultBudget: 16000, Kind: "openai"},
	{Name: "fake", Title: "Fake (offline demo/testing)", DefaultModel: "fake-1",
		DefaultBudget: 60000, Local: true, Kind: "fake"},
}

// Lookup finds a provider by name.
func Lookup(name string) (ProviderInfo, bool) {
	for _, p := range Providers {
		if p.Name == name {
			return p, true
		}
	}
	return ProviderInfo{}, false
}

// ProviderNames lists provider names.
func ProviderNames() []string {
	out := make([]string, len(Providers))
	for i, p := range Providers {
		out[i] = p.Name
	}
	return out
}
