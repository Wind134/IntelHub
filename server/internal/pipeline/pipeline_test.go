package pipeline

import (
	"math/bits"
	"testing"

	"intel-hub/internal/config"
	"intel-hub/internal/model"
)

func TestCanonicalizeURLAndT2S(t *testing.T) {
	got := CanonicalizeURL("https://Example.com/news/123?utm_source=x&mod=hp&id=42#sec")
	if got != "https://example.com/news/123?id=42" {
		t.Fatalf("unexpected canonical url: %s", got)
	}
	zh := CleanText("<b>美國聯準會宣布緊急降息，英偉達股價暴漲</b>", true)
	if zh != "美国联准会宣布紧急降息，英伟达股价暴涨" {
		t.Fatalf("unexpected t2s output: %s", zh)
	}
}

func TestSimHashNearDuplicate(t *testing.T) {
	h1 := ComputeSimHash64("英伟达盘后急跌6%：美国司法部向英伟达发出反垄断调查传票")
	h2 := ComputeSimHash64("英伟达盘后急跌6%，美国司法部向英伟达发出反垄断调查传票！")
	dist := bits.OnesCount64(h1 ^ h2)
	if dist > 3 {
		t.Fatalf("expected near-duplicate Hamming distance <= 3, got %d", dist)
	}
}

func TestRuleFilter(t *testing.T) {
	rf := NewRuleFilter(config.FilterConfig{
		ExcludeKeywords: []string{"美食", "旅游"},
	})
	if pass, _ := rf.Match(model.RawItem{Title: "周末美食旅游攻略推荐", Summary: "好吃好玩"}); pass {
		t.Fatalf("expected excluded lifestyle item to be dropped")
	}
	if pass, _ := rf.Match(model.RawItem{Title: "OpenAI 突发宣布新一代旗舰模型发布", Summary: "API 价格腰斩"}); !pass {
		t.Fatalf("expected AI breaking news to pass")
	}
}
