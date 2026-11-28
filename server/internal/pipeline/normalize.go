package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var (
	htmlBreakRe  = regexp.MustCompile(`(?i)<\s*br\s*/?\s*>`)
	htmlBlockRe  = regexp.MustCompile(`(?i)</\s*(p|div|li|h[1-6]|blockquote|tr|section|article)\s*>`)
	htmlListRe   = regexp.MustCompile(`(?i)<\s*li[^>]*>`)
	htmlTagRe    = regexp.MustCompile(`<[^>]+>`)
	multiSpaceRe = regexp.MustCompile(`\s+`)
	horizSpaceRe = regexp.MustCompile(`[ \t\f\v]+`)
	multiBlankRe = regexp.MustCompile(`\n{3,}`)
	dropParams   = map[string]bool{
		"utm_source": true, "utm_medium": true, "utm_campaign": true,
		"utm_term": true, "utm_content": true, "utm_id": true,
		"mod": true, "f": true, "oc": true, "fbclid": true,
		"gclid": true, "spm": true, "from": true, "ref": true,
	}
)

// CleanText strips HTML tags, unescapes entities, collapses whitespace into a single line, and optionally converts Traditional Chinese.
func CleanText(s string, convertT2S bool) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = multiSpaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	if convertT2S {
		s = TraditionalToSimplified(s)
	}
	return s
}

// CleanSummaryText strips HTML while preserving natural paragraph breaks (<p>, <br>, <div>, <li>) for clean reading.
func CleanSummaryText(s string, convertT2S bool) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = htmlListRe.ReplaceAllString(s, "\n• ")
	s = htmlBreakRe.ReplaceAllString(s, "\n")
	s = htmlBlockRe.ReplaceAllString(s, "\n\n")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		l := strings.TrimSpace(horizSpaceRe.ReplaceAllString(line, " "))
		if l == "" {
			if len(cleaned) > 0 && cleaned[len(cleaned)-1] != "" {
				cleaned = append(cleaned, "")
			}
			continue
		}
		cleaned = append(cleaned, l)
	}
	s = strings.TrimSpace(strings.Join(cleaned, "\n"))
	s = multiBlankRe.ReplaceAllString(s, "\n\n")
	if convertT2S {
		s = TraditionalToSimplified(s)
	}
	return s
}

// CanonicalizeURL removes tracking query parameters and normalizes scheme/host.
func CanonicalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if dropParams[lk] || strings.HasPrefix(lk, "utm_") {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// SHA256Hex returns lowercase hex SHA256 digest.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ComputeSimHash64 computes a 64-bit semantic fingerprint over normalized CJK bigrams and ASCII words.
// Two rewritten headlines about the same breaking news typically have Hamming distance <= 3.
func ComputeSimHash64(text string) uint64 {
	var v [64]int
	features := extractFeatures(text)
	if len(features) == 0 {
		return 0
	}
	for feat, weight := range features {
		h := fnv64a(feat)
		for i := 0; i < 64; i++ {
			if (h>>uint(i))&1 == 1 {
				v[i] += weight
			} else {
				v[i] -= weight
			}
		}
	}
	var fp uint64
	for i := 0; i < 64; i++ {
		if v[i] > 0 {
			fp |= 1 << uint(i)
		}
	}
	return fp
}

func extractFeatures(text string) map[string]int {
	feats := make(map[string]int, 64)
	runes := []rune(strings.ToLower(text))
	var asciiBuf strings.Builder
	var cjkRun []rune

	flushASCII := func() {
		if asciiBuf.Len() >= 2 {
			feats[asciiBuf.String()]++
		}
		asciiBuf.Reset()
	}
	flushCJK := func() {
		for i := 0; i < len(cjkRun)-1; i++ {
			feats[string(cjkRun[i:i+2])]++
		}
		if len(cjkRun) == 1 {
			feats[string(cjkRun[0])]++
		}
		cjkRun = cjkRun[:0]
	}

	for _, r := range runes {
		switch {
		case isCJK(r):
			flushASCII()
			cjkRun = append(cjkRun, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			asciiBuf.WriteRune(r)
		default:
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return feats
}

func fnv64a(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

// Common news/finance/tech Traditional-to-Simplified character mapping (zero external C dependency).
var t2sMap = map[rune]rune{
	'國': '国', '際': '际', '經': '经', '濟': '济', '財': '财', '發': '发', '報': '报', '導': '导',
	'聞': '闻', '華': '华', '爾': '尔', '見': '见', '證': '证', '銀': '银', '聯': '联', '儲': '储',
	'價': '价', '漲': '涨', '億': '亿', '萬': '万', '業': '业', '務': '务', '體': '体', '積': '积',
	'電': '电', '腦': '脑', '網': '网', '絡': '络', '軟': '软', '開': '开', '權': '权', '斷': '断',
	'調': '调', '訴': '诉', '訟': '讼', '罰': '罚', '購': '购', '併': '并', '賣': '卖', '買': '买',
	'場': '场', '會': '会', '議': '议', '長': '长', '總': '总', '統': '统', '軍': '军', '戰': '战',
	'衝': '冲', '機': '机', '構': '构', '關': '关', '稅': '税', '貿': '贸', '廠': '厂', '產': '产',
	'線': '线', '鏈': '链', '應': '应', '對': '对', '與': '与', '為': '为', '從': '从', '將': '将',
	'點': '点', '數': '数', '據': '据', '預': '预', '測': '测', '顯': '显', '達': '达', '創': '创',
	'紀': '纪', '錄': '录', '緊': '紧', '聲': '声', '稱': '称', '專': '专', '員': '员', '辭': '辞',
	'職': '职', '離': '离', '啟': '启', '動': '动', '閉': '闭', '鎖': '锁', '區': '区', '塊': '块',
	'幣': '币', '資': '资', '匯': '汇', '債': '债', '險': '险', '臺': '台', '灣': '湾', '東': '东',
	'亞': '亚', '歐': '欧', '烏': '乌', '襲': '袭', '擊': '击', '災': '灾', '難': '难', '響': '响',
	'進': '进', '還': '还', '這': '这', '個': '个', '們': '们', '來': '来', '時': '时', '間': '间',
	'後': '后', '裡': '里', '邊': '边', '過': '过', '無': '无', '實': '实', '現': '现', '變': '变',
	'問': '问', '題': '题', '結': '结', '計': '计', '劃': '划', '設': '设', '備': '备', '號': '号',
	'碼': '码', '說': '说', '話': '话', '語': '语', '認': '认', '識': '识', '評': '评', '論': '论',
	'讓': '让', '給': '给', '續': '续', '級': '级', '約': '约', '組': '组', '織': '织', '終': '终',
	'維': '维', '護': '护', '獲': '获', '獨': '独', '佔': '占', '費': '费', '質': '质', '貨': '货',
	'車': '车', '轉': '转', '輪': '轮', '輸': '输', '運': '运', '連': '连', '選': '选', '舉': '举',
	'醫': '医', '藥': '药', '釋': '释', '鐵': '铁', '銅': '铜', '銷': '销', '門': '门', '陽': '阳',
	'陰': '阴', '陸': '陆', '雲': '云', '韓': '韩', '頁': '页', '項': '项', '順': '顺', '頭': '头',
	'額': '额', '風': '风', '飛': '飞', '馬': '马', '驗': '验', '黨': '党', '齊': '齐', '龍': '龙',
	'偉': '伟', '準': '准', '宣': '宣', '佈': '布', '內': '内', '換': '换', '試': '试',
}

func TraditionalToSimplified(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if mapped, ok := t2sMap[r]; ok {
			b.WriteRune(mapped)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
