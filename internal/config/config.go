// Package config loads and validates configuration from environment variables.
package config

import (
	"cmp"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for the summarize service.
type Config struct {
	// Server
	Port     string
	DataDir  string
	LogLevel string

	// Defaults
	DefaultEngine string
	DefaultPrompt string

	// Engine binaries
	PiBin  string
	AgyBin string

	// Engine default models
	PiModel  string
	AgyModel string

	// Limits
	RunTimeout    time.Duration
	MaxInputChars int

	// YouTube
	YtdlpBin        string
	TranscriptLangs string
	YtdlpTimeout    time.Duration

	// NATS
	NATSHost    string
	NATSPort    int
	NATSSubject string

	// Cache
	CacheTTL time.Duration

	// MCP auth
	MCPAuthMode     string // none, static, oauth
	MCPAPIKey       string // for static mode
	MCPOAuthISS     string // expected issuer for oauth mode
	MCPOAuthJWKS    string // JWKS URL for oauth mode
	MCPOAuthAud     string // expected audience for oauth mode (optional)
	MCPAnonymousRef string // deployment-stable first-party ID for none mode
	MCPEnable       bool   // whether MCP endpoint is mounted

	AgentFeedbackKey         string
	AgentFeedbackEndpoint    string
	AgentFeedbackRuntimeHint string
	SummarizeInstallationRef string
}

// DefaultPrompt is the built-in fallback summarization instruction.
const DefaultPrompt = `Summarize the following content clearly and accurately.
Include:
- A concise overview
- Key points
- Important details, decisions, or claims
- Action items if any

Do not invent facts that are not present in the source.`

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	port := cmp.Or(os.Getenv("PORT"), "8080")
	dataDir := cmp.Or(os.Getenv("DATA_DIR"), "./data")
	logLevel := cmp.Or(os.Getenv("LOG_LEVEL"), "info")

	defaultEngine := cmp.Or(os.Getenv("SUMMARIZE_ENGINE"), "pi")
	defaultPrompt := cmp.Or(os.Getenv("SUMMARIZE_PROMPT"), DefaultPrompt)

	piBin := cmp.Or(os.Getenv("SUMMARIZE_PI_BIN"), "pi")
	agyBin := cmp.Or(os.Getenv("SUMMARIZE_AGY_BIN"), "agy")
	piModel := os.Getenv("SUMMARIZE_PI_MODEL")
	agyModel := os.Getenv("SUMMARIZE_AGY_MODEL")

	runTimeoutStr := cmp.Or(os.Getenv("SUMMARIZE_RUN_TIMEOUT"), "15m")
	runTimeout, err := time.ParseDuration(runTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("invalid SUMMARIZE_RUN_TIMEOUT: %w", err)
	}

	maxInputChars := 100000
	if s := os.Getenv("SUMMARIZE_MAX_INPUT_CHARS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			maxInputChars = n
		}
	}

	ytdlpBin := cmp.Or(os.Getenv("SUMMARIZE_YTDLP_BIN"), "yt-dlp")
	transcriptLangs := cmp.Or(os.Getenv("SUMMARIZE_TRANSCRIPT_LANGS"), "id-orig,id,en.*,en")

	ytdlpTimeoutStr := cmp.Or(os.Getenv("SUMMARIZE_YTDLP_TIMEOUT"), "60s")
	ytdlpTimeout, err := time.ParseDuration(ytdlpTimeoutStr)
	if err != nil {
		return nil, fmt.Errorf("invalid SUMMARIZE_YTDLP_TIMEOUT: %w", err)
	}

	natsHost := cmp.Or(os.Getenv("NATS_HOST"), "127.0.0.1")
	natsPortStr := cmp.Or(os.Getenv("NATS_PORT"), "-1")
	natsPort, err := strconv.Atoi(natsPortStr)
	if err != nil {
		return nil, fmt.Errorf("invalid NATS_PORT: %w", err)
	}

	natsSubject := cmp.Or(os.Getenv("NATS_SUBJECT_SUMMARY_REQUESTED"), "summaries.requested")

	cacheTTLStr := cmp.Or(os.Getenv("SUMMARIZE_CACHE_TTL"), "168h") // 7 days
	cacheTTL, err := time.ParseDuration(cacheTTLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid SUMMARIZE_CACHE_TTL: %w", err)
	}

	// MCP auth configuration
	mcpAuthMode := cmp.Or(os.Getenv("MCP_AUTH_MODE"), "none")
	mcpAPIKey := os.Getenv("MCP_API_KEY")
	mcpOAuthISS := os.Getenv("MCP_OAUTH_ISSUER")
	mcpOAuthJWKS := os.Getenv("MCP_OAUTH_JWKS_URL")
	mcpOAuthAud := os.Getenv("MCP_OAUTH_AUDIENCE")
	installationRef := os.Getenv("SUMMARIZE_INSTALLATION_REF")
	if installationRef == "" {
		installationRef = os.Getenv("MCP_ANONYMOUS_REF")
	}
	mcpEnable := os.Getenv("MCP_ENABLE") != "0" // enabled by default

	cfg := &Config{
		Port:                     port,
		DataDir:                  dataDir,
		LogLevel:                 logLevel,
		DefaultEngine:            defaultEngine,
		DefaultPrompt:            defaultPrompt,
		PiBin:                    piBin,
		AgyBin:                   agyBin,
		PiModel:                  piModel,
		AgyModel:                 agyModel,
		RunTimeout:               runTimeout,
		MaxInputChars:            maxInputChars,
		YtdlpBin:                 ytdlpBin,
		TranscriptLangs:          transcriptLangs,
		YtdlpTimeout:             ytdlpTimeout,
		NATSHost:                 natsHost,
		NATSPort:                 natsPort,
		NATSSubject:              natsSubject,
		CacheTTL:                 cacheTTL,
		MCPAuthMode:              mcpAuthMode,
		MCPAPIKey:                mcpAPIKey,
		MCPOAuthISS:              mcpOAuthISS,
		MCPOAuthJWKS:             mcpOAuthJWKS,
		MCPOAuthAud:              mcpOAuthAud,
		MCPAnonymousRef:          installationRef,
		MCPEnable:                mcpEnable,
		AgentFeedbackKey:         os.Getenv("AGENT_FEEDBACK_KEY"),
		AgentFeedbackEndpoint:    os.Getenv("AGENT_FEEDBACK_ENDPOINT"),
		AgentFeedbackRuntimeHint: os.Getenv("AGENT_FEEDBACK_RUNTIME_HINT"),
		SummarizeInstallationRef: installationRef,
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate ensures configuration is valid.
func (c *Config) Validate() error {
	if c.DefaultEngine != "pi" && c.DefaultEngine != "agy" {
		return fmt.Errorf("SUMMARIZE_ENGINE must be 'pi' or 'agy', got %q", c.DefaultEngine)
	}
	if c.MaxInputChars <= 0 {
		return fmt.Errorf("SUMMARIZE_MAX_INPUT_CHARS must be positive")
	}
	if c.RunTimeout <= 0 {
		return fmt.Errorf("SUMMARIZE_RUN_TIMEOUT must be positive")
	}
	if c.YtdlpTimeout <= 0 {
		return fmt.Errorf("SUMMARIZE_YTDLP_TIMEOUT must be positive")
	}
	if c.CacheTTL <= 0 {
		return fmt.Errorf("SUMMARIZE_CACHE_TTL must be positive")
	}
	switch c.MCPAuthMode {
	case "", "none", "static", "oauth":
		// valid
	default:
		return fmt.Errorf("MCP_AUTH_MODE must be 'none', 'static', or 'oauth', got %q", c.MCPAuthMode)
	}
	if c.MCPAuthMode == "static" && c.MCPAPIKey == "" {
		return fmt.Errorf("MCP_API_KEY is required when MCP_AUTH_MODE=static")
	}
	if c.MCPAuthMode == "oauth" {
		if c.MCPOAuthISS == "" {
			return fmt.Errorf("MCP_OAUTH_ISSUER is required when MCP_AUTH_MODE=oauth")
		}
		if c.MCPOAuthJWKS == "" {
			return fmt.Errorf("MCP_OAUTH_JWKS_URL is required when MCP_AUTH_MODE=oauth")
		}
	}
	return nil
}
