package pipeline

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	articleFetchClient = &http.Client{Timeout: 5 * time.Second}
	reScriptStyle      = regexp.MustCompile(`(?is)<(script|style|noscript|svg|header|footer|nav|aside)[^>]*>.*?</(script|style|noscript|svg|header|footer|nav|aside)>`)
	reArticleTag       = regexp.MustCompile(`(?is)<(article|main)[^>]*>(.*?)</(article|main)>`)
	reParaTag          = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
)

// shouldFetchArticleExcerpt triggers only for short RSS/web teasers that have a direct article URL.
func shouldFetchArticleExcerpt(sourceType, rawURL, summary string) bool {
	if sourceType == "twitter_api" || sourceType == "finnhub_ws" || sourceType == "status_atom" || sourceType == "cisa_kev" {
		return false
	}
	u := strings.ToLower(strings.TrimSpace(rawURL))
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return false
	}
	if strings.Contains(u, "x.com/") || strings.Contains(u, "twitter.com/") || strings.Contains(u, "t.co/") {
		return false
	}
	return len([]rune(strings.TrimSpace(summary))) < 90
}

// fetchArticleExcerpt performs a fast best-effort HTML paragraph extraction when an RSS feed only supplies a short teaser.
func fetchArticleExcerpt(ctx context.Context, rawURL string, tradZh bool) string {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")

	resp, err := articleFetchClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ""
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if ct != "" && !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml") {
		return ""
	}

	rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil || len(rawBytes) == 0 {
		return ""
	}
	return extractParagraphsFromHTML(string(rawBytes), tradZh)
}

func extractParagraphsFromHTML(htmlStr string, tradZh bool) string {
	cleaned := reScriptStyle.ReplaceAllString(htmlStr, " ")
	scope := cleaned
	if m := reArticleTag.FindStringSubmatch(cleaned); len(m) >= 3 && len(m[2]) > 180 {
		scope = m[2]
	}

	matches := reParaTag.FindAllStringSubmatch(scope, 40)
	if len(matches) == 0 && scope != cleaned {
		matches = reParaTag.FindAllStringSubmatch(cleaned, 40)
	}

	var paras []string
	totalRunes := 0
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		txt := CleanText(m[1], tradZh)
		rLen := len([]rune(txt))
		if rLen < 18 {
			continue
		}
		lower := strings.ToLower(txt)
		if strings.Contains(lower, "cookie") || strings.Contains(lower, "copyright") ||
			strings.Contains(txt, "扫码关注") || strings.Contains(txt, "阅读原文") ||
			strings.Contains(txt, "版权声明") || strings.Contains(txt, "免责声明") {
			continue
		}
		paras = append(paras, txt)
		totalRunes += rLen
		if totalRunes >= 1100 || len(paras) >= 8 {
			break
		}
	}
	if totalRunes < 60 {
		return ""
	}
	return strings.Join(paras, "\n\n")
}
