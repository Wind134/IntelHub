package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Budget     BudgetConfig     `yaml:"budget"`
	LLM        LLMConfig        `yaml:"llm"`
	Search     SearchConfig     `yaml:"search"`
	TwitterAPI TwitterAPIConfig `yaml:"twitter_api"`
	Finnhub    FinnhubConfig    `yaml:"finnhub"`
	Filters    FilterConfig     `yaml:"filters"`
	Notify     NotifyConfig     `yaml:"notify"`
	Feeds      []FeedConfig     `yaml:"feeds"`
}

type ServerConfig struct {
	ListenAddr     string `yaml:"listen_addr"`
	CapitalWebAddr string `yaml:"capital_web_addr"`
	DBPath         string `yaml:"db_path"`
	APIToken       string `yaml:"api_token"`
	PublicURL      string `yaml:"public_url"`
	BackupDir      string `yaml:"backup_dir"`
	BackupRemote   string `yaml:"backup_remote"`
	TushareToken   string `yaml:"tushare_token"`
	SECUserAgent   string `yaml:"sec_user_agent"`
}

type BudgetConfig struct {
	MonthlyCapUSD float64 `yaml:"monthly_cap_usd"`
	TwitterCapUSD float64 `yaml:"twitter_cap_usd"`
}

type LLMConfig struct {
	Provider      string              `yaml:"provider"`
	BaseURL       string              `yaml:"base_url"`
	APIKey        string              `yaml:"api_key"`
	FastModel     string              `yaml:"fast_model"`
	FallbackModel string              `yaml:"fallback_model"`
	StrongModel   string              `yaml:"strong_model"`
	TimeoutSec    int                 `yaml:"timeout_sec"`
	Providers     []LLMProviderPreset `yaml:"providers"`
}

type LLMProviderPreset struct {
	Name      string   `yaml:"name"`
	BaseURL   string   `yaml:"base_url"`
	APIKeyEnv string   `yaml:"api_key_env"`
	APIKey    string   `yaml:"api_key"`
	Models    []string `yaml:"models"`
}

type SearchConfig struct {
	BraveAPIKey  string `yaml:"brave_api_key"`
	TavilyAPIKey string `yaml:"tavily_api_key"`
}

type TwitterAPIConfig struct {
	Enabled           bool     `yaml:"enabled"`
	APIKey            string   `yaml:"api_key"`
	ActiveIntervalSec int      `yaml:"active_interval_sec"`
	QuietIntervalSec  int      `yaml:"quiet_interval_sec"`
	Accounts          []string `yaml:"accounts"`
	QueryOverride     string   `yaml:"query_override"`
}

type FinnhubConfig struct {
	Enabled           bool     `yaml:"enabled"`
	APIKey            string   `yaml:"api_key"`
	Symbols           []string `yaml:"symbols"`
	ETFSymbols        []string `yaml:"etf_symbols"`
	WindowMinutes     int      `yaml:"window_minutes"`
	StockThresholdPct float64  `yaml:"stock_threshold_pct"`
	ETFThresholdPct   float64  `yaml:"etf_threshold_pct"`
	NewsIntervalSec   int      `yaml:"news_interval_sec"`
}

type FilterConfig struct {
	ExcludeKeywords  []string `yaml:"exclude_keywords"`
	IncludeKeywords  []string `yaml:"include_keywords"`
	BreakingVerbs    []string `yaml:"breaking_verbs"`
	DomainEntities   []string `yaml:"domain_entities"`
	MinTextLength    int      `yaml:"min_text_length"`
	SimHashThreshold int      `yaml:"simhash_threshold"`
	CooldownMinutes  int      `yaml:"cooldown_minutes"`
}

type NotifyConfig struct {
	FeishuWebhook         string `yaml:"feishu_webhook"`
	FeishuSecret          string `yaml:"feishu_secret"`
	FeishuAppID           string `yaml:"feishu_app_id"`
	FeishuAppSecret       string `yaml:"feishu_app_secret"`
	FeishuChatID          string `yaml:"feishu_chat_id"`
	FeishuMinScore        int    `yaml:"feishu_min_score"`
	FCMServiceAccountPath string `yaml:"fcm_service_account_path"`
	FCMProjectID          string `yaml:"fcm_project_id"`
	WeixinP1Mirror        bool   `yaml:"weixin_p1_mirror"`
}

type FeedConfig struct {
	Name          string `yaml:"name"`
	Label         string `yaml:"label"`
	Type          string `yaml:"type"` // rss | status_atom | sec_atom | wscn_lives | cls_telegraph | cisa_kev | usgs_quake
	Category      string `yaml:"category"`
	URL           string `yaml:"url"`
	IntervalSec   int    `yaml:"interval_sec"`
	Enabled       bool   `yaml:"enabled"`
	BypassRules   bool   `yaml:"bypass_rules"`
	TraditionalZh bool   `yaml:"traditional_zh"`
}

// DefaultConfig returns sensible production defaults for the primary server.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			ListenAddr:     "127.0.0.1:8787",
			CapitalWebAddr: "127.0.0.1:8788",
			DBPath:         "~/.hermes/intel/intel_hub.db",
			BackupDir:      "~/.hermes/intel/backups",
			SECUserAgent:   "InformationHubResearch/1.0 (research@informationhub.app)",
		},
		Budget: BudgetConfig{
			MonthlyCapUSD: 2.80,
			TwitterCapUSD: 2.50,
		},
		LLM: LLMConfig{
			Provider:      "opencode-go",
			BaseURL:       "https://opencode.ai/zen/go/v1",
			FastModel:     "deepseek-v4.1-flash",
			FallbackModel: "qwen3.8-flash",
			StrongModel:   "deepseek-v4-pro",
			TimeoutSec:    25,
		},
		TwitterAPI: TwitterAPIConfig{
			Enabled:           false,
			ActiveIntervalSec: 120,
			QuietIntervalSec:  300,
		},
		Finnhub: FinnhubConfig{
			Enabled:           false,
			Symbols:           []string{"NVDA", "AAPL", "MSFT", "GOOGL", "META", "AMZN", "TSLA", "TSM", "AVGO", "AMD", "ASML", "ARM", "PLTR", "QQQ", "SPY", "SOXX"},
			ETFSymbols:        []string{"QQQ", "SPY", "SOXX", "DIA", "IWM"},
			WindowMinutes:     5,
			StockThresholdPct: 3.5,
			ETFThresholdPct:   1.5,
			NewsIntervalSec:   120,
		},
		Filters: FilterConfig{
			MinTextLength:    15,
			SimHashThreshold: 3,
			CooldownMinutes:  30,
		},
		Notify: NotifyConfig{
			FeishuMinScore: 8,
		},
	}
}

// Load reads optional .env files, YAML config, and overrides secrets from environment variables.
func Load(path string, envFiles ...string) (*Config, error) {
	for _, ef := range envFiles {
		_ = loadDotEnv(expandPath(ef))
	}

	cfg := DefaultConfig()
	if path != "" {
		expanded := expandPath(path)
		data, err := os.ReadFile(expanded)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", expanded, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse yaml %s: %w", expanded, err)
		}
	}

	applyEnvOverrides(cfg)
	cfg.Server.DBPath = expandPath(cfg.Server.DBPath)
	cfg.Server.BackupDir = expandPath(cfg.Server.BackupDir)
	cfg.Notify.FCMServiceAccountPath = expandPath(cfg.Notify.FCMServiceAccountPath)

	if cfg.Budget.MonthlyCapUSD <= 0 {
		cfg.Budget.MonthlyCapUSD = 2.80
	}
	if cfg.Budget.TwitterCapUSD <= 0 {
		cfg.Budget.TwitterCapUSD = 2.50
	}
	if cfg.Filters.SimHashThreshold <= 0 {
		cfg.Filters.SimHashThreshold = 3
	}
	if cfg.Filters.CooldownMinutes <= 0 {
		cfg.Filters.CooldownMinutes = 30
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	envStr := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}
	if v := envStr("INTEL_LISTEN_ADDR"); v != "" {
		cfg.Server.ListenAddr = v
	}
	if v := envStr("CAPITAL_WEB_ADDR"); v != "" {
		cfg.Server.CapitalWebAddr = v
	}
	if v := envStr("TUSHARE_TOKEN"); v != "" {
		cfg.Server.TushareToken = v
	}
	if v := envStr("SEC_USER_AGENT"); v != "" {
		cfg.Server.SECUserAgent = v
	}
	if v := envStr("INTEL_DB_PATH"); v != "" {
		cfg.Server.DBPath = v
	}
	if v := envStr("INTEL_API_TOKEN"); v != "" {
		cfg.Server.APIToken = v
	}
	if v := envStr("INTEL_PUBLIC_URL"); v != "" {
		cfg.Server.PublicURL = v
	}
	if v := envStr("INTEL_LLM_BASE_URL", "OPENCODE_GO_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := envStr("INTEL_LLM_API_KEY", "OPENCODE_GO_API_KEY", "OPENAI_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := envStr("BRAVE_API_KEY"); v != "" {
		cfg.Search.BraveAPIKey = v
	}
	if v := envStr("TAVILY_API_KEY"); v != "" {
		cfg.Search.TavilyAPIKey = v
	}
	if v := envStr("TWITTERAPI_IO_KEY"); v != "" {
		cfg.TwitterAPI.APIKey = v
		cfg.TwitterAPI.Enabled = true
	}
	if v := envStr("FINNHUB_API_KEY"); v != "" {
		cfg.Finnhub.APIKey = v
		cfg.Finnhub.Enabled = true
	}
	if v := envStr("FEISHU_WEBHOOK_URL"); v != "" {
		cfg.Notify.FeishuWebhook = v
	}
	if v := envStr("FEISHU_WEBHOOK_SECRET"); v != "" {
		cfg.Notify.FeishuSecret = v
	}
	if v := envStr("FEISHU_APP_ID"); v != "" && cfg.Notify.FeishuAppID == "" {
		cfg.Notify.FeishuAppID = v
	}
	if v := envStr("FEISHU_APP_SECRET"); v != "" && cfg.Notify.FeishuAppSecret == "" {
		cfg.Notify.FeishuAppSecret = v
	}
	if v := envStr("INTEL_FEISHU_CHAT_ID", "FEISHU_HOME_CHANNEL"); v != "" && cfg.Notify.FeishuChatID == "" {
		cfg.Notify.FeishuChatID = v
	}
	if v := envStr("FCM_SERVICE_ACCOUNT_PATH"); v != "" {
		cfg.Notify.FCMServiceAccountPath = v
	}
	if v := envStr("FCM_PROJECT_ID"); v != "" {
		cfg.Notify.FCMProjectID = v
	}
}

func expandPath(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" && os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	return sc.Err()
}
