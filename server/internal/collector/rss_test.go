package collector

import (
	"strings"
	"testing"
)

func TestClampPublishedTS(t *testing.T) {
	now := int64(1795800000)
	// Normal past timestamp kept intact
	if got := clampPublishedTS(now-600, now); got != now-600 {
		t.Fatalf("expected past ts preserved, got %d", got)
	}
	// Slight clock skew within 5 min kept intact
	if got := clampPublishedTS(now+120, now); got != now+120 {
		t.Fatalf("expected slight skew preserved, got %d", got)
	}
	// Future scheduled maintenance or +8h timezone bug clamped to now
	if got := clampPublishedTS(now+8*3600, now); got != now {
		t.Fatalf("expected future ts clamped to now, got %d", got)
	}
	if got := clampPublishedTS(0, now); got != now {
		t.Fatalf("expected zero ts clamped to now, got %d", got)
	}
}

func TestIsRoutineStatusMaintenance(t *testing.T) {
	if !isRoutineStatusMaintenance("CMB (Colombo) on 2026-12-09", "We will be performing scheduled maintenance in CMB (Colombo) datacenter.") {
		t.Fatalf("expected routine Cloudflare scheduled maintenance to be filtered")
	}
	if isRoutineStatusMaintenance("Cloudflare Global Network Outage", "Major outage affecting API and edge routing across regions.") {
		t.Fatalf("expected real outage to pass")
	}
	if isRoutineStatusMaintenance("Scheduled Maintenance causing Major Outage", "Unexpected service disruption during maintenance.") {
		t.Fatalf("expected maintenance escalated to outage to pass")
	}
}

func TestBuildTweetRawItem(t *testing.T) {
	// 1. 纯表情与单词回复必须直接过滤
	for _, junk := range []string{"❤️", "Yes", "Yup", "It is very useful", "Exactly. https://t.co/abc"} {
		if _, keep := buildTweetRawItem(twitterTweet{
			ID:     "1",
			Text:   junk,
			Author: twitterAuthor{UserName: "elonmusk"},
		}, false); keep {
			t.Fatalf("expected low-signal tweet %q to be filtered out", junk)
		}
	}

	// 2. 带完整原推上下文的引用评论应当保留并自动补全原推背景
	item, keep := buildTweetRawItem(twitterTweet{
		ID:     "2",
		Text:   "True",
		Author: twitterAuthor{UserName: "elonmusk"},
		QuotedTweet: &twitterQuoted{
			ID:     "100",
			Text:   "SpaceX Starship Flight 9 just received FAA launch license for next week orbital test.",
			Author: twitterAuthor{UserName: "SpaceX"},
		},
	}, false)
	if !keep {
		t.Fatalf("expected substantive quoted tweet to be kept")
	}
	if !strings.Contains(item.Summary, "原推上下文") || !strings.Contains(item.Summary, "Starship Flight 9") {
		t.Fatalf("expected quoted context embedded in summary, got: %s", item.Summary)
	}
}
