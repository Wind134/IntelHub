package pipeline

import (
	"strings"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
)

// RuleFilter implements L3 zero-cost hard-rule filtering (blacklist + domain/breaking whitelist).
type RuleFilter struct {
	excludes       []string
	includes       []string
	breakingVerbs  []string
	domainEntities []string
	minLen         int
}

func NewRuleFilter(cfg config.FilterConfig) *RuleFilter {
	lowerAll := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, s := range in {
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	minLen := cfg.MinTextLength
	if minLen <= 0 {
		minLen = 15
	}
	rf := &RuleFilter{
		excludes:       lowerAll(cfg.ExcludeKeywords),
		includes:       lowerAll(cfg.IncludeKeywords),
		breakingVerbs:  lowerAll(cfg.BreakingVerbs),
		domainEntities: lowerAll(cfg.DomainEntities),
		minLen:         minLen,
	}
	if len(rf.breakingVerbs) == 0 {
		rf.breakingVerbs = defaultBreakingVerbs
	}
	if len(rf.domainEntities) == 0 {
		rf.domainEntities = defaultDomainEntities
	}
	return rf
}

// Match returns (pass, reason). Items with BypassRuleFilter=true always pass L3.
func (f *RuleFilter) Match(item model.RawItem) (bool, string) {
	if item.BypassRuleFilter {
		return true, "bypass_fast_lane"
	}
	combined := strings.ToLower(strings.TrimSpace(item.Title + " " + item.Summary))
	if len([]rune(strings.TrimSpace(item.Title))) < 6 && len([]rune(combined)) < f.minLen {
		return false, "too_short"
	}
	for _, ex := range f.excludes {
		if strings.Contains(combined, ex) {
			return false, "excluded:" + ex
		}
	}
	if len(f.includes) > 0 {
		matched := false
		for _, inc := range f.includes {
			if strings.Contains(combined, inc) {
				matched = true
				break
			}
		}
		if !matched {
			return false, "not_in_include_whitelist"
		}
	}

	hasEntity := false
	for _, ent := range f.domainEntities {
		if strings.Contains(combined, ent) {
			hasEntity = true
			break
		}
	}
	hasVerb := false
	for _, v := range f.breakingVerbs {
		if strings.Contains(combined, v) {
			hasVerb = true
			break
		}
	}
	if hasEntity || hasVerb {
		return true, "rule_matched"
	}
	return false, "no_domain_or_breaking_signal"
}

var defaultBreakingVerbs = []string{
	"突发", "重磅", "暴跌", "暴涨", "跳水", "熔断", "停牌", "宕机", "瘫痪", "中断",
	"收购", "并购", "制裁", "禁令", "起诉", "传票", "反垄断", "罚款", "调查", "辞职",
	"解雇", "开源", "发布", "降价", "腰斩", "零日", "漏洞", "黑客", "袭击", "地震",
	"加息", "降息", "非农", "cpi", "pce", "财报", "业绩", "超预期", "暴雷",
	"breaking", "urgent", "plunge", "surge", "crash", "halt", "outage", "acquire",
	"merger", "sanction", "ban", "antitrust", "subpoena", "lawsuit", "resign",
	"fired", "release", "launch", "open-source", "open weights", "zero-day", "breach",
	"fed", "fomc", "earnings", "guidance",
}

var defaultDomainEntities = []string{
	"openai", "anthropic", "claude", "deepseek", "qwen", "gemini", "google", "alphabet",
	"meta", "llama", "nvidia", "nvda", "tsmc", "台积电", "英伟达", "微软", "microsoft",
	"apple", "苹果", "amazon", "aws", "tesla", "特斯拉", "amd", "asml", "broadcom",
	"字节", "阿里", "腾讯", "华为", "小米", "百度", "美团", "拼多多", "cloudflare",
	"github", "hugging face", "huggingface", "美联储", "央行", "纳斯达克", "标普",
	"nasdaq", "s&p", "bitcoin", "btc", "sec", "doj", "cisa", "sam altman", "musk",
	"马斯克", "黄仁勋", "鲍威尔", "川普", "特朗普", "trump",
}
