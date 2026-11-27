package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"intel-hub/internal/model"
)

var shanghaiLoc = time.FixedZone("Asia/Shanghai", 8*3600)

// Store manages WAL-mode SQLite persistence with separated write/read pools.
type Store struct {
	dbPath  string
	writeDB *sql.DB
	readDB  *sql.DB
	writeMu sync.Mutex
}

// Open initializes the SQLite database in WAL mode and runs schema migrations.
func Open(dbPath string) (*Store, error) {
	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	dsn := dbPath
	if dbPath == ":memory:" {
		dsn = "file:intel_hub_mem?mode=memory&cache=shared"
	}

	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open write sqlite: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("open read sqlite: %w", err)
	}
	readDB.SetMaxOpenConns(4)
	readDB.SetMaxIdleConns(4)

	s := &Store{
		dbPath:  dbPath,
		writeDB: writeDB,
		readDB:  readDB,
	}

	if err := s.applyPragmas(writeDB); err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.applyPragmas(readDB); err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	var errs []error
	if s.readDB != nil {
		if err := s.readDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.writeDB != nil {
		if err := s.writeDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Store) applyPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA temp_store = MEMORY;",
		"PRAGMA cache_size = -8000;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("exec %s: %w", p, err)
		}
	}
	return nil
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS dedup_fingerprints (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    source_uid    TEXT NOT NULL UNIQUE,
    url_hash      TEXT NOT NULL,
    simhash       INTEGER NOT NULL,
    band0         INTEGER NOT NULL,
    band1         INTEGER NOT NULL,
    band2         INTEGER NOT NULL,
    band3         INTEGER NOT NULL,
    title_preview TEXT NOT NULL DEFAULT '',
    first_seen_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dedup_url ON dedup_fingerprints(url_hash);
CREATE INDEX IF NOT EXISTS idx_dedup_band0 ON dedup_fingerprints(band0, first_seen_at);
CREATE INDEX IF NOT EXISTS idx_dedup_band1 ON dedup_fingerprints(band1, first_seen_at);
CREATE INDEX IF NOT EXISTS idx_dedup_band2 ON dedup_fingerprints(band2, first_seen_at);
CREATE INDEX IF NOT EXISTS idx_dedup_band3 ON dedup_fingerprints(band3, first_seen_at);
CREATE INDEX IF NOT EXISTS idx_dedup_seen ON dedup_fingerprints(first_seen_at);

CREATE TABLE IF NOT EXISTS intel_events (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    event_code          TEXT NOT NULL UNIQUE,
    source_name         TEXT NOT NULL,
    category            TEXT NOT NULL,
    raw_title           TEXT NOT NULL,
    zh_title            TEXT NOT NULL,
    summary             TEXT NOT NULL,
    impact_analysis     TEXT NOT NULL DEFAULT '',
    canonical_url       TEXT NOT NULL,
    entities_json       TEXT NOT NULL DEFAULT '[]',
    quote_snapshot_json TEXT NOT NULL DEFAULT '{}',
    score               INTEGER NOT NULL,
    tier                TEXT NOT NULL,
    is_bookmarked       INTEGER NOT NULL DEFAULT 0,
    user_note           TEXT NOT NULL DEFAULT '',
    push_status         TEXT NOT NULL,
    verification_status TEXT NOT NULL DEFAULT 'none',
    verification_note   TEXT NOT NULL DEFAULT '',
    feishu_message_id   TEXT NOT NULL DEFAULT '',
    search_tokens       TEXT NOT NULL DEFAULT '',
    published_at        INTEGER NOT NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_events_created ON intel_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_published ON intel_events(published_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_cat_score_time ON intel_events(category, score DESC, published_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_bookmarked ON intel_events(is_bookmarked, published_at DESC) WHERE is_bookmarked = 1;

CREATE TABLE IF NOT EXISTS event_entities (
    entity_norm  TEXT NOT NULL,
    event_id     INTEGER NOT NULL REFERENCES intel_events(id) ON DELETE CASCADE,
    published_at INTEGER NOT NULL,
    score        INTEGER NOT NULL,
    PRIMARY KEY (entity_norm, event_id)
);
CREATE INDEX IF NOT EXISTS idx_entity_timeline ON event_entities(entity_norm, published_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS intel_events_fts USING fts5(
    event_code, zh_title, raw_title, summary, impact_analysis, entities_json, search_tokens,
    content='intel_events', content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER IF NOT EXISTS intel_events_ai AFTER INSERT ON intel_events BEGIN
    INSERT INTO intel_events_fts(rowid, event_code, zh_title, raw_title, summary, impact_analysis, entities_json, search_tokens)
    VALUES (new.id, new.event_code, new.zh_title, new.raw_title, new.summary, new.impact_analysis, new.entities_json, new.search_tokens);
END;

CREATE TRIGGER IF NOT EXISTS intel_events_ad AFTER DELETE ON intel_events BEGIN
    INSERT INTO intel_events_fts(intel_events_fts, rowid, event_code, zh_title, raw_title, summary, impact_analysis, entities_json, search_tokens)
    VALUES('delete', old.id, old.event_code, old.zh_title, old.raw_title, old.summary, old.impact_analysis, old.entities_json, old.search_tokens);
END;

CREATE TRIGGER IF NOT EXISTS intel_events_au AFTER UPDATE ON intel_events BEGIN
    INSERT INTO intel_events_fts(intel_events_fts, rowid, event_code, zh_title, raw_title, summary, impact_analysis, entities_json, search_tokens)
    VALUES('delete', old.id, old.event_code, old.zh_title, old.raw_title, old.summary, old.impact_analysis, old.entities_json, old.search_tokens);
    INSERT INTO intel_events_fts(rowid, event_code, zh_title, raw_title, summary, impact_analysis, entities_json, search_tokens)
    VALUES (new.id, new.event_code, new.zh_title, new.raw_title, new.summary, new.impact_analysis, new.entities_json, new.search_tokens);
END;

CREATE TABLE IF NOT EXISTS digest_queue (
    event_id     INTEGER PRIMARY KEY REFERENCES intel_events(id) ON DELETE CASCADE,
    category     TEXT NOT NULL,
    bullet_point TEXT NOT NULL,
    digested_at  INTEGER DEFAULT NULL,
    created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_digest_pending ON digest_queue(digested_at, category, created_at);

CREATE TABLE IF NOT EXISTS daily_digests (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    digest_date TEXT NOT NULL UNIQUE,
    title       TEXT NOT NULL,
    content_md  TEXT NOT NULL,
    event_count INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS event_threads (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id   INTEGER NOT NULL REFERENCES intel_events(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    content_md TEXT NOT NULL,
    model_used TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_threads_event ON event_threads(event_id, created_at ASC);

CREATE TABLE IF NOT EXISTS client_devices (
    device_id       TEXT PRIMARY KEY,
    device_name     TEXT NOT NULL,
    platform        TEXT NOT NULL,
    fcm_token       TEXT NOT NULL DEFAULT '',
    min_push_score  INTEGER NOT NULL DEFAULT 8,
    categories_json TEXT NOT NULL DEFAULT '["finance","ai","bigtech","world"]',
    quiet_hours     TEXT NOT NULL DEFAULT '',
    last_seen_at    INTEGER NOT NULL,
    created_at      INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cooldown_locks (
    entity_key    TEXT PRIMARY KEY,
    last_event_id INTEGER NOT NULL,
    last_score    INTEGER NOT NULL DEFAULT 8,
    hit_count     INTEGER NOT NULL DEFAULT 1,
    expires_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS source_states (
    source_name       TEXT PRIMARY KEY,
    source_type       TEXT NOT NULL,
    cursor_value      TEXT NOT NULL DEFAULT '',
    etag              TEXT NOT NULL DEFAULT '',
    last_modified     TEXT NOT NULL DEFAULT '',
    last_success_at   INTEGER NOT NULL DEFAULT 0,
    last_error_at     INTEGER NOT NULL DEFAULT 0,
    last_error_msg    TEXT NOT NULL DEFAULT '',
    consecutive_fails INTEGER NOT NULL DEFAULT 0,
    alerted_offline   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS api_budget_ledger (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    month_key     TEXT NOT NULL,
    provider      TEXT NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 0,
    units_used    INTEGER NOT NULL DEFAULT 0,
    estimated_usd REAL NOT NULL DEFAULT 0.0,
    updated_at    INTEGER NOT NULL,
    UNIQUE(month_key, provider)
);

CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
`
	if _, err := s.writeDB.Exec(schema); err != nil {
		return err
	}
	// Safe additive column migration for existing databases
	_, _ = s.writeDB.Exec(`ALTER TABLE intel_events ADD COLUMN feishu_message_id TEXT NOT NULL DEFAULT ''`)
	return nil
}

func simhashBands(h uint64) (int64, int64, int64, int64) {
	return int64((h >> 48) & 0xFFFF),
		int64((h >> 32) & 0xFFFF),
		int64((h >> 16) & 0xFFFF),
		int64(h & 0xFFFF)
}

// CheckAndRecordFingerprint checks exact source_uid, canonical url_hash, and 64-bit SimHash
// Hamming distance <= maxHamming within the last 48h. Returns (isDup, reason, err).
func (s *Store) CheckAndRecordFingerprint(ctx context.Context, sourceUID, urlHash string, simhash uint64, titlePreview string, maxHamming int) (bool, string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var exists int
	if err := s.writeDB.QueryRowContext(ctx, `SELECT 1 FROM dedup_fingerprints WHERE source_uid = ? LIMIT 1`, sourceUID).Scan(&exists); err == nil {
		return true, "source_uid", nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, "", err
	}

	if urlHash != "" {
		if err := s.writeDB.QueryRowContext(ctx, `SELECT 1 FROM dedup_fingerprints WHERE url_hash = ? LIMIT 1`, urlHash).Scan(&exists); err == nil {
			return true, "url_hash", nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return false, "", err
		}
	}

	b0, b1, b2, b3 := simhashBands(simhash)
	if simhash != 0 && maxHamming >= 0 {
		cutoff := time.Now().Add(-48 * time.Hour).Unix()
		rows, err := s.writeDB.QueryContext(ctx, `
			SELECT simhash FROM dedup_fingerprints
			WHERE first_seen_at >= ? AND (band0 = ? OR band1 = ? OR band2 = ? OR band3 = ?)
			LIMIT 200
		`, cutoff, b0, b1, b2, b3)
		if err != nil {
			return false, "", err
		}
		var dupSim bool
		for rows.Next() {
			var existing int64
			if err := rows.Scan(&existing); err == nil {
				if bits.OnesCount64(simhash^uint64(existing)) <= maxHamming {
					dupSim = true
					break
				}
			}
		}
		_ = rows.Close()
		if dupSim {
			return true, "simhash", nil
		}
	}

	now := time.Now().Unix()
	if len(titlePreview) > 120 {
		titlePreview = titlePreview[:120]
	}
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT OR IGNORE INTO dedup_fingerprints
		(source_uid, url_hash, simhash, band0, band1, band2, band3, title_preview, first_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sourceUID, urlHash, int64(simhash), b0, b1, b2, b3, titlePreview, now)
	return false, "", err
}

// RecordFingerprintOnly inserts a watermark fingerprint without SimHash comparison (used for migration/baseline).
func (s *Store) RecordFingerprintOnly(ctx context.Context, sourceUID, urlHash string, simhash uint64, titlePreview string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	b0, b1, b2, b3 := simhashBands(simhash)
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT OR IGNORE INTO dedup_fingerprints
		(source_uid, url_hash, simhash, band0, band1, band2, band3, title_preview, first_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sourceUID, urlHash, int64(simhash), b0, b1, b2, b3, titlePreview, time.Now().Unix())
	return err
}

// PruneOldFingerprints deletes dedup fingerprints older than retentionDays (intel_events is never deleted).
func (s *Store) PruneOldFingerprints(ctx context.Context, retentionDays int) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	res, err := s.writeDB.ExecContext(ctx, `DELETE FROM dedup_fingerprints WHERE first_seen_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// InsertEvent persists a scored intelligence event permanently, indexes entities + CJK FTS5, and queues P2 for digest.
func (s *Store) InsertEvent(ctx context.Context, ev *model.IntelEvent) (*model.IntelEvent, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now()
	nowUnix := now.Unix()
	if ev.PublishedAt == 0 {
		ev.PublishedAt = nowUnix
	}
	ev.CreatedAt = nowUnix
	ev.UpdatedAt = nowUnix
	if ev.Tier == "" {
		ev.Tier = model.TierFromScore(ev.Score)
	}
	if ev.VerificationStatus == "" {
		ev.VerificationStatus = "none"
	}
	if ev.QuoteSnapshot == "" {
		ev.QuoteSnapshot = "{}"
	}
	entitiesJSON, _ := json.Marshal(ev.Entities)
	if len(ev.Entities) == 0 {
		entitiesJSON = []byte("[]")
	}

	searchTokens := BuildCJKSearchTokens(ev.EventCode, ev.ZhTitle, ev.RawTitle, ev.Summary, ev.ImpactAnalysis, strings.Join(ev.Entities, " "))

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Placeholder unique code before we know autoincrement ID, then finalize EVT-YYYYMMDD-NNNN.
	tempCode := fmt.Sprintf("TMP-%d", now.UnixNano())
	if ev.EventCode != "" {
		tempCode = ev.EventCode
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO intel_events (
			event_code, source_name, category, raw_title, zh_title, summary, impact_analysis,
			canonical_url, entities_json, quote_snapshot_json, score, tier, is_bookmarked, user_note,
			push_status, verification_status, verification_note, search_tokens, published_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, tempCode, ev.SourceName, ev.Category, ev.RawTitle, ev.ZhTitle, ev.Summary, ev.ImpactAnalysis,
		ev.CanonicalURL, string(entitiesJSON), ev.QuoteSnapshot, ev.Score, ev.Tier, boolToInt(ev.IsBookmarked), ev.UserNote,
		ev.PushStatus, ev.VerificationStatus, ev.VerificationNote, searchTokens, ev.PublishedAt, ev.CreatedAt, ev.UpdatedAt)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	ev.ID = id

	if ev.EventCode == "" {
		dateStr := now.In(shanghaiLoc).Format("20060102")
		ev.EventCode = fmt.Sprintf("EVT-%s-%04d", dateStr, id)
		updatedTokens := BuildCJKSearchTokens(ev.EventCode, ev.ZhTitle, ev.RawTitle, ev.Summary, ev.ImpactAnalysis, strings.Join(ev.Entities, " "))
		if _, err := tx.ExecContext(ctx, `UPDATE intel_events SET event_code = ?, search_tokens = ? WHERE id = ?`, ev.EventCode, updatedTokens, id); err != nil {
			return nil, err
		}
	}

	for _, ent := range ev.Entities {
		norm := strings.ToUpper(strings.TrimSpace(ent))
		if norm == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO event_entities (entity_norm, event_id, published_at, score)
			VALUES (?, ?, ?, ?)
		`, norm, id, ev.PublishedAt, ev.Score); err != nil {
			return nil, err
		}
	}

	if ev.Score >= 5 && ev.Score <= 7 {
		bullet := ev.ZhTitle
		if ev.ImpactAnalysis != "" {
			bullet = ev.ZhTitle + " — " + ev.ImpactAnalysis
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO digest_queue (event_id, category, bullet_point, created_at)
			VALUES (?, ?, ?, ?)
		`, id, ev.Category, bullet, nowUnix); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ev, nil
}

// UpdateEventVerification updates the multi-source fact-check status on an event.
func (s *Store) UpdateEventVerification(ctx context.Context, eventID int64, status, note string) (*model.IntelEvent, error) {
	s.writeMu.Lock()
	_, err := s.writeDB.ExecContext(ctx, `
		UPDATE intel_events
		SET verification_status = ?, verification_note = ?, updated_at = ?
		WHERE id = ?
	`, status, note, time.Now().Unix(), eventID)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.GetEventByID(ctx, eventID)
}

// SetFeishuMessageID records the Feishu message_id returned when sending the original P1 alert card.
func (s *Store) SetFeishuMessageID(ctx context.Context, eventID int64, messageID string) error {
	if messageID == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `
		UPDATE intel_events SET feishu_message_id = ?, updated_at = ? WHERE id = ?
	`, messageID, time.Now().Unix(), eventID)
	return err
}

// SetBookmark updates bookmark status and user note for a historical event.
func (s *Store) SetBookmark(ctx context.Context, eventCode string, bookmarked bool, userNote *string) (*model.IntelEvent, error) {
	s.writeMu.Lock()
	var err error
	if userNote != nil {
		_, err = s.writeDB.ExecContext(ctx, `
			UPDATE intel_events SET is_bookmarked = ?, user_note = ?, updated_at = ? WHERE event_code = ?
		`, boolToInt(bookmarked), *userNote, time.Now().Unix(), eventCode)
	} else {
		_, err = s.writeDB.ExecContext(ctx, `
			UPDATE intel_events SET is_bookmarked = ?, updated_at = ? WHERE event_code = ?
		`, boolToInt(bookmarked), time.Now().Unix(), eventCode)
	}
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.GetEventByCode(ctx, eventCode)
}

func (s *Store) GetEventByID(ctx context.Context, id int64) (*model.IntelEvent, error) {
	row := s.readDB.QueryRowContext(ctx, selectEventCols+` FROM intel_events e WHERE e.id = ?`, id)
	return scanEvent(row, false)
}

func (s *Store) GetEventByCode(ctx context.Context, code string) (*model.IntelEvent, error) {
	row := s.readDB.QueryRowContext(ctx, selectEventCols+` FROM intel_events e WHERE e.event_code = ?`, code)
	return scanEvent(row, false)
}

const selectEventCols = `
SELECT e.id, e.event_code, e.source_name, e.category, e.raw_title, e.zh_title, e.summary,
       e.impact_analysis, e.canonical_url, e.entities_json, e.quote_snapshot_json,
       e.score, e.tier, e.is_bookmarked, e.user_note, e.push_status,
       e.verification_status, e.verification_note, e.feishu_message_id, e.published_at, e.created_at, e.updated_at`

// ListEvents performs cursor-based or multi-faceted historical queries across the full archive.
func (s *Store) ListEvents(ctx context.Context, f model.EventFilter) ([]model.IntelEvent, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	useFTS := strings.TrimSpace(f.Query) != ""
	var sb strings.Builder
	var args []any

	if useFTS {
		ftsQuery := BuildFTSQuery(f.Query)
		if ftsQuery == "" {
			useFTS = false
		} else {
			sb.WriteString(selectEventCols)
			sb.WriteString(`, snippet(intel_events_fts, 1, '<mark>', '</mark>', '...', 24) AS snip `)
			sb.WriteString(`FROM intel_events_fts JOIN intel_events e ON e.id = intel_events_fts.rowid WHERE intel_events_fts MATCH ?`)
			args = append(args, ftsQuery)
		}
	}
	if !useFTS {
		sb.WriteString(selectEventCols)
		sb.WriteString(` FROM intel_events e WHERE 1=1`)
	}

	if len(f.Categories) > 0 {
		placeholders := make([]string, len(f.Categories))
		for i, c := range f.Categories {
			placeholders[i] = "?"
			args = append(args, c)
		}
		sb.WriteString(` AND e.category IN (` + strings.Join(placeholders, ",") + `)`)
	}
	if f.MinScore > 0 {
		sb.WriteString(` AND e.score >= ?`)
		args = append(args, f.MinScore)
	}
	if f.MaxScore > 0 {
		sb.WriteString(` AND e.score <= ?`)
		args = append(args, f.MaxScore)
	}
	if f.FromTS > 0 {
		sb.WriteString(` AND e.published_at >= ?`)
		args = append(args, f.FromTS)
	}
	if f.ToTS > 0 {
		sb.WriteString(` AND e.published_at <= ?`)
		args = append(args, f.ToTS)
	}
	if f.Source != "" {
		sb.WriteString(` AND e.source_name = ?`)
		args = append(args, f.Source)
	}
	if f.BookmarkedOnly {
		sb.WriteString(` AND e.is_bookmarked = 1`)
	}
	if f.VerifiedOnly {
		sb.WriteString(` AND e.verification_status = 'confirmed'`)
	}
	if f.Entity != "" {
		sb.WriteString(` AND EXISTS (SELECT 1 FROM event_entities ee WHERE ee.event_id = e.id AND ee.entity_norm = ?)`)
		args = append(args, strings.ToUpper(strings.TrimSpace(f.Entity)))
	}
	if f.BeforeID > 0 {
		sb.WriteString(` AND e.id < ?`)
		args = append(args, f.BeforeID)
	}
	if f.AfterID > 0 {
		sb.WriteString(` AND e.id > ?`)
		args = append(args, f.AfterID)
	}

	switch f.Sort {
	case "time_asc":
		sb.WriteString(` ORDER BY e.published_at ASC, e.id ASC`)
	case "score_desc":
		sb.WriteString(` ORDER BY e.score DESC, e.published_at DESC`)
	case "relevance":
		if useFTS {
			sb.WriteString(` ORDER BY bm25(intel_events_fts, 12.0, 10.0, 6.0, 2.0, 5.0, 8.0, 4.0) ASC, e.published_at DESC`)
		} else {
			sb.WriteString(` ORDER BY e.published_at DESC, e.id DESC`)
		}
	default:
		if f.AfterID > 0 {
			sb.WriteString(` ORDER BY e.id ASC`)
		} else {
			sb.WriteString(` ORDER BY e.published_at DESC, e.id DESC`)
		}
	}

	sb.WriteString(` LIMIT ?`)
	args = append(args, limit)

	rows, err := s.readDB.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.IntelEvent
	for rows.Next() {
		ev, err := scanEvent(rows, useFTS)
		if err != nil {
			return nil, err
		}
		events = append(events, *ev)
	}
	return events, rows.Err()
}

// GetEntityTimeline returns the full historical event timeline for a single normalized entity.
func (s *Store) GetEntityTimeline(ctx context.Context, entity string, fromTS, toTS int64, limit int) ([]model.IntelEvent, error) {
	return s.ListEvents(ctx, model.EventFilter{
		Entity: entity,
		FromTS: fromTS,
		ToTS:   toTS,
		Sort:   "time_desc",
		Limit:  limit,
	})
}

// CheckAndSetCooldown enforces per-entity P1 alert cooldown (default 30 minutes).
// Returns (cooledDown, err). A new event with score >= last_score+1 breaks cooldown.
func (s *Store) CheckAndSetCooldown(ctx context.Context, entityKey string, eventID int64, score int, ttl time.Duration) (bool, error) {
	if entityKey == "" {
		return false, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now().Unix()
	var lastScore int
	var expiresAt int64
	err := s.writeDB.QueryRowContext(ctx, `SELECT last_score, expires_at FROM cooldown_locks WHERE entity_key = ?`, entityKey).Scan(&lastScore, &expiresAt)
	if err == nil && expiresAt > now && score < lastScore+1 {
		_, _ = s.writeDB.ExecContext(ctx, `UPDATE cooldown_locks SET hit_count = hit_count + 1 WHERE entity_key = ?`, entityKey)
		return true, nil
	}

	_, err = s.writeDB.ExecContext(ctx, `
		INSERT INTO cooldown_locks (entity_key, last_event_id, last_score, hit_count, expires_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(entity_key) DO UPDATE SET
			last_event_id = excluded.last_event_id,
			last_score = excluded.last_score,
			hit_count = 1,
			expires_at = excluded.expires_at
	`, entityKey, eventID, score, now+int64(ttl.Seconds()))
	return false, err
}

// PendingDigestItems returns un-digested 5-7 score items for the daily 21:00 briefing.
func (s *Store) PendingDigestItems(ctx context.Context) ([]model.DigestItem, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT d.event_id, e.event_code, d.category, e.zh_title, d.bullet_point, e.canonical_url, e.score, e.published_at, d.created_at
		FROM digest_queue d
		JOIN intel_events e ON e.id = d.event_id
		WHERE d.digested_at IS NULL
		ORDER BY d.category ASC, e.score DESC, e.published_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []model.DigestItem
	for rows.Next() {
		var it model.DigestItem
		if err := rows.Scan(&it.EventID, &it.EventCode, &it.Category, &it.ZhTitle, &it.BulletPoint, &it.URL, &it.Score, &it.PublishedAt, &it.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// AckDigestItems marks pending digest items created up to untilTS as digested.
func (s *Store) AckDigestItems(ctx context.Context, untilTS int64) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if untilTS == 0 {
		untilTS = time.Now().Unix()
	}
	res, err := s.writeDB.ExecContext(ctx, `
		UPDATE digest_queue SET digested_at = ? WHERE digested_at IS NULL AND created_at <= ?
	`, time.Now().Unix(), untilTS)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SaveDailyDigest stores a generated daily executive digest markdown.
func (s *Store) SaveDailyDigest(ctx context.Context, d model.DailyDigest) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if d.DigestDate == "" {
		d.DigestDate = time.Now().In(shanghaiLoc).Format("2006-01-02")
	}
	if d.CreatedAt == 0 {
		d.CreatedAt = time.Now().Unix()
	}
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO daily_digests (digest_date, title, content_md, event_count, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(digest_date) DO UPDATE SET
			title = excluded.title,
			content_md = excluded.content_md,
			event_count = excluded.event_count,
			created_at = excluded.created_at
	`, d.DigestDate, d.Title, d.ContentMD, d.EventCount, d.CreatedAt)
	return err
}

// ListDigests lists archived daily digests, optionally filtered by month ("YYYY-MM").
func (s *Store) ListDigests(ctx context.Context, month string, limit int) ([]model.DailyDigest, error) {
	if limit <= 0 || limit > 120 {
		limit = 31
	}
	var rows *sql.Rows
	var err error
	if month != "" {
		rows, err = s.readDB.QueryContext(ctx, `
			SELECT id, digest_date, title, content_md, event_count, created_at
			FROM daily_digests WHERE digest_date LIKE ? ORDER BY digest_date DESC LIMIT ?
		`, month+"%", limit)
	} else {
		rows, err = s.readDB.QueryContext(ctx, `
			SELECT id, digest_date, title, content_md, event_count, created_at
			FROM daily_digests ORDER BY digest_date DESC LIMIT ?
		`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.DailyDigest
	for rows.Next() {
		var d model.DailyDigest
		if err := rows.Scan(&d.ID, &d.DigestDate, &d.Title, &d.ContentMD, &d.EventCount, &d.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// AppendThreadMessage saves a user or assistant message in an event's AI conversation history.
func (s *Store) AppendThreadMessage(ctx context.Context, eventID int64, role, contentMD, modelUsed string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO event_threads (event_id, role, content_md, model_used, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, eventID, role, contentMD, modelUsed, time.Now().Unix())
	return err
}

// GetEventThreads returns the full AI follow-up conversation for an event.
func (s *Store) GetEventThreads(ctx context.Context, eventID int64) ([]model.EventThread, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT id, event_id, role, content_md, model_used, created_at
		FROM event_threads WHERE event_id = ? ORDER BY id ASC
	`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.EventThread
	for rows.Next() {
		var m model.EventThread
		if err := rows.Scan(&m.ID, &m.EventID, &m.Role, &m.ContentMD, &m.ModelUsed, &m.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// UpsertDevice registers or updates a Windows/Android client device and its push preferences.
func (s *Store) UpsertDevice(ctx context.Context, d model.ClientDevice) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	now := time.Now().Unix()
	if d.MinPushScore <= 0 {
		d.MinPushScore = 8
	}
	if len(d.Categories) == 0 {
		d.Categories = []string{"finance", "ai", "bigtech", "world"}
	}
	catsJSON, _ := json.Marshal(d.Categories)
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO client_devices (device_id, device_name, platform, fcm_token, min_push_score, categories_json, quiet_hours, last_seen_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			device_name = excluded.device_name,
			platform = excluded.platform,
			fcm_token = CASE WHEN excluded.fcm_token != '' THEN excluded.fcm_token ELSE client_devices.fcm_token END,
			min_push_score = excluded.min_push_score,
			categories_json = excluded.categories_json,
			quiet_hours = excluded.quiet_hours,
			last_seen_at = excluded.last_seen_at
	`, d.DeviceID, d.DeviceName, d.Platform, d.FCMToken, d.MinPushScore, string(catsJSON), d.QuietHours, now, now)
	return err
}

// ListFCMDevices returns registered Android devices with non-empty FCM tokens.
func (s *Store) ListFCMDevices(ctx context.Context) ([]model.ClientDevice, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT device_id, device_name, platform, fcm_token, min_push_score, categories_json, quiet_hours, last_seen_at, created_at
		FROM client_devices WHERE fcm_token != ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.ClientDevice
	for rows.Next() {
		var d model.ClientDevice
		var catsRaw string
		if err := rows.Scan(&d.DeviceID, &d.DeviceName, &d.Platform, &d.FCMToken, &d.MinPushScore, &catsRaw, &d.QuietHours, &d.LastSeenAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(catsRaw), &d.Categories)
		list = append(list, d)
	}
	return list, rows.Err()
}

// GetSourceState retrieves cursor and ETag state for a collector source.
func (s *Store) GetSourceState(ctx context.Context, name string) (*model.SourceState, error) {
	var st model.SourceState
	var alerted int
	err := s.readDB.QueryRowContext(ctx, `
		SELECT source_name, source_type, cursor_value, etag, last_modified, last_success_at, last_error_at, last_error_msg, consecutive_fails, alerted_offline
		FROM source_states WHERE source_name = ?
	`, name).Scan(&st.SourceName, &st.SourceType, &st.CursorValue, &st.ETag, &st.LastModified, &st.LastSuccessAt, &st.LastErrorAt, &st.LastErrorMsg, &st.ConsecutiveFails, &alerted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st.AlertedOffline = alerted != 0
	return &st, nil
}

// UpsertSourceSuccess marks a collector fetch as healthy and saves its cursor/ETag.
func (s *Store) UpsertSourceSuccess(ctx context.Context, name, sourceType, cursor, etag, lastMod string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO source_states (source_name, source_type, cursor_value, etag, last_modified, last_success_at, consecutive_fails, alerted_offline)
		VALUES (?, ?, ?, ?, ?, ?, 0, 0)
		ON CONFLICT(source_name) DO UPDATE SET
			source_type = excluded.source_type,
			cursor_value = CASE WHEN excluded.cursor_value != '' THEN excluded.cursor_value ELSE source_states.cursor_value END,
			etag = CASE WHEN excluded.etag != '' THEN excluded.etag ELSE source_states.etag END,
			last_modified = CASE WHEN excluded.last_modified != '' THEN excluded.last_modified ELSE source_states.last_modified END,
			last_success_at = excluded.last_success_at,
			consecutive_fails = 0,
			alerted_offline = 0
	`, name, sourceType, cursor, etag, lastMod, time.Now().Unix())
	return err
}

// UpsertSourceError records a collector fetch failure without advancing its cursor.
func (s *Store) UpsertSourceError(ctx context.Context, name, sourceType, errMsg string) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	now := time.Now().Unix()
	if len(errMsg) > 300 {
		errMsg = errMsg[:300]
	}
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO source_states (source_name, source_type, last_error_at, last_error_msg, consecutive_fails)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(source_name) DO UPDATE SET
			last_error_at = excluded.last_error_at,
			last_error_msg = excluded.last_error_msg,
			consecutive_fails = source_states.consecutive_fails + 1
	`, name, sourceType, now, errMsg)
	if err != nil {
		return 0, err
	}
	var fails int
	_ = s.writeDB.QueryRowContext(ctx, `SELECT consecutive_fails FROM source_states WHERE source_name = ?`, name).Scan(&fails)
	return fails, nil
}

// ListSourceStates lists health rows for all configured sources.
func (s *Store) ListSourceStates(ctx context.Context) ([]model.SourceState, error) {
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT source_name, source_type, cursor_value, etag, last_modified, last_success_at, last_error_at, last_error_msg, consecutive_fails, alerted_offline
		FROM source_states ORDER BY source_name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []model.SourceState
	for rows.Next() {
		var st model.SourceState
		var alerted int
		if err := rows.Scan(&st.SourceName, &st.SourceType, &st.CursorValue, &st.ETag, &st.LastModified, &st.LastSuccessAt, &st.LastErrorAt, &st.LastErrorMsg, &st.ConsecutiveFails, &alerted); err != nil {
			return nil, err
		}
		st.AlertedOffline = alerted != 0
		list = append(list, st)
	}
	return list, rows.Err()
}

// CurrentMonthKey returns YYYY-MM in UTC+8.
func CurrentMonthKey() string {
	return time.Now().In(shanghaiLoc).Format("2006-01")
}

// RecordAPIBudget increments request count, units, and estimated USD spend for the current month.
func (s *Store) RecordAPIBudget(ctx context.Context, provider string, reqs, units int64, usd float64) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	month := CurrentMonthKey()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO api_budget_ledger (month_key, provider, request_count, units_used, estimated_usd, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(month_key, provider) DO UPDATE SET
			request_count = api_budget_ledger.request_count + excluded.request_count,
			units_used = api_budget_ledger.units_used + excluded.units_used,
			estimated_usd = api_budget_ledger.estimated_usd + excluded.estimated_usd,
			updated_at = excluded.updated_at
	`, month, provider, reqs, units, usd, time.Now().Unix())
	return err
}

// GetMonthlyBudget returns current month spend breakdown and whether the hard cap is tripped.
func (s *Store) GetMonthlyBudget(ctx context.Context, monthlyCap float64) (model.BudgetSummary, error) {
	month := CurrentMonthKey()
	rows, err := s.readDB.QueryContext(ctx, `
		SELECT month_key, provider, request_count, units_used, estimated_usd, updated_at
		FROM api_budget_ledger WHERE month_key = ? ORDER BY estimated_usd DESC
	`, month)
	if err != nil {
		return model.BudgetSummary{}, err
	}
	defer rows.Close()

	summary := model.BudgetSummary{
		MonthKey:      month,
		MonthlyCapUSD: monthlyCap,
	}
	for rows.Next() {
		var r model.BudgetRecord
		if err := rows.Scan(&r.MonthKey, &r.Provider, &r.RequestCount, &r.UnitsUsed, &r.EstimatedUSD, &r.UpdatedAt); err != nil {
			return summary, err
		}
		summary.TotalUSD += r.EstimatedUSD
		summary.Providers = append(summary.Providers, r)
	}
	summary.Tripped = monthlyCap > 0 && summary.TotalUSD >= monthlyCap
	return summary, rows.Err()
}

// GetSystemOverview builds the dashboard stats payload.
func (s *Store) GetSystemOverview(ctx context.Context, monthlyCap float64, activeWS int) (model.SystemOverview, error) {
	var ov model.SystemOverview
	ov.ActiveWSClients = activeWS
	ov.ServerTime = time.Now().Unix()

	_ = s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM intel_events`).Scan(&ov.TotalEvents)
	_ = s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM intel_events WHERE score >= 8 AND created_at >= ?`, time.Now().Add(-24*time.Hour).Unix()).Scan(&ov.P1Events24h)
	_ = s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM intel_events WHERE is_bookmarked = 1`).Scan(&ov.BookmarkedEvents)
	_ = s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM daily_digests`).Scan(&ov.TotalDigests)
	_ = s.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM client_devices`).Scan(&ov.RegisteredDevices)

	if s.dbPath != ":memory:" {
		if fi, err := os.Stat(s.dbPath); err == nil {
			ov.DBSizeBytes = fi.Size()
		}
	}
	ov.Sources, _ = s.ListSourceStates(ctx)
	ov.Budget, _ = s.GetMonthlyBudget(ctx, monthlyCap)
	return ov, nil
}

// GetSetting retrieves a JSON or string runtime setting by key.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var val string
	err := s.readDB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return val, err
}

// SetSetting persists a runtime setting by key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, key, value, time.Now().Unix())
	return err
}

// BackupToFile creates a consistent hot snapshot using SQLite VACUUM INTO.
func (s *Store) BackupToFile(ctx context.Context, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(destPath)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	escaped := strings.ReplaceAll(destPath, "'", "''")
	_, err := s.writeDB.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", escaped))
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(r rowScanner, hasSnippet bool) (*model.IntelEvent, error) {
	var ev model.IntelEvent
	var entitiesRaw string
	var bookmarked int
	var err error
	if hasSnippet {
		err = r.Scan(
			&ev.ID, &ev.EventCode, &ev.SourceName, &ev.Category, &ev.RawTitle, &ev.ZhTitle, &ev.Summary,
			&ev.ImpactAnalysis, &ev.CanonicalURL, &entitiesRaw, &ev.QuoteSnapshot,
			&ev.Score, &ev.Tier, &bookmarked, &ev.UserNote, &ev.PushStatus,
			&ev.VerificationStatus, &ev.VerificationNote, &ev.FeishuMessageID, &ev.PublishedAt, &ev.CreatedAt, &ev.UpdatedAt, &ev.Snippet,
		)
	} else {
		err = r.Scan(
			&ev.ID, &ev.EventCode, &ev.SourceName, &ev.Category, &ev.RawTitle, &ev.ZhTitle, &ev.Summary,
			&ev.ImpactAnalysis, &ev.CanonicalURL, &entitiesRaw, &ev.QuoteSnapshot,
			&ev.Score, &ev.Tier, &bookmarked, &ev.UserNote, &ev.PushStatus,
			&ev.VerificationStatus, &ev.VerificationNote, &ev.FeishuMessageID, &ev.PublishedAt, &ev.CreatedAt, &ev.UpdatedAt,
		)
	}
	if err != nil {
		return nil, err
	}
	ev.IsBookmarked = bookmarked != 0
	_ = json.Unmarshal([]byte(entitiesRaw), &ev.Entities)
	if ev.Entities == nil {
		ev.Entities = []string{}
	}
	return &ev, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
