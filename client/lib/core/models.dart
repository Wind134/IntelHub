import 'dart:convert';

class IntelEvent {
  final int id;
  final String eventCode;
  final String sourceName;
  final String category;
  final String rawTitle;
  final String zhTitle;
  final String summary;
  final String impactAnalysis;
  final String canonicalUrl;
  final List<String> entities;
  final Map<String, dynamic> quoteSnapshot;
  final int score;
  final String tier;
  final bool isBookmarked;
  final String userNote;
  final String pushStatus;
  final String verificationStatus;
  final String verificationNote;
  final int publishedAt;
  final int createdAt;
  final String snippet;

  const IntelEvent({
    required this.id,
    required this.eventCode,
    required this.sourceName,
    required this.category,
    required this.rawTitle,
    required this.zhTitle,
    required this.summary,
    required this.impactAnalysis,
    required this.canonicalUrl,
    required this.entities,
    required this.quoteSnapshot,
    required this.score,
    required this.tier,
    required this.isBookmarked,
    required this.userNote,
    required this.pushStatus,
    required this.verificationStatus,
    required this.verificationNote,
    required this.publishedAt,
    required this.createdAt,
    this.snippet = '',
  });

  factory IntelEvent.fromJson(Map<String, dynamic> j) {
    Map<String, dynamic> quote = {};
    final qRaw = j['quote_snapshot'];
    if (qRaw is String && qRaw.isNotEmpty && qRaw != '{}') {
      try {
        final decoded = jsonDecode(qRaw);
        if (decoded is Map<String, dynamic>) quote = decoded;
      } catch (_) {}
    } else if (qRaw is Map<String, dynamic>) {
      quote = qRaw;
    }

    return IntelEvent(
      id: (j['id'] as num?)?.toInt() ?? 0,
      eventCode: (j['event_code'] as String?) ?? '',
      sourceName: (j['source_name'] as String?) ?? '',
      category: (j['category'] as String?) ?? 'bigtech',
      rawTitle: (j['raw_title'] as String?) ?? '',
      zhTitle: (j['zh_title'] as String?) ?? '',
      summary: (j['summary'] as String?) ?? '',
      impactAnalysis: (j['impact_analysis'] as String?) ?? '',
      canonicalUrl: (j['canonical_url'] as String?) ?? '',
      entities: ((j['entities'] as List?) ?? const []).map((e) => e.toString()).toList(),
      quoteSnapshot: quote,
      score: (j['score'] as num?)?.toInt() ?? 5,
      tier: (j['tier'] as String?) ?? 'P2_DIGEST',
      isBookmarked: (j['is_bookmarked'] as bool?) ?? false,
      userNote: (j['user_note'] as String?) ?? '',
      pushStatus: (j['push_status'] as String?) ?? '',
      verificationStatus: (j['verification_status'] as String?) ?? 'none',
      verificationNote: (j['verification_note'] as String?) ?? '',
      publishedAt: (j['published_at'] as num?)?.toInt() ?? 0,
      createdAt: (j['created_at'] as num?)?.toInt() ?? 0,
      snippet: (j['snippet'] as String?) ?? '',
    );
  }

  IntelEvent copyWith({
    bool? isBookmarked,
    String? userNote,
    String? verificationStatus,
    String? verificationNote,
  }) {
    return IntelEvent(
      id: id,
      eventCode: eventCode,
      sourceName: sourceName,
      category: category,
      rawTitle: rawTitle,
      zhTitle: zhTitle,
      summary: summary,
      impactAnalysis: impactAnalysis,
      canonicalUrl: canonicalUrl,
      entities: entities,
      quoteSnapshot: quoteSnapshot,
      score: score,
      tier: tier,
      isBookmarked: isBookmarked ?? this.isBookmarked,
      userNote: userNote ?? this.userNote,
      pushStatus: pushStatus,
      verificationStatus: verificationStatus ?? this.verificationStatus,
      verificationNote: verificationNote ?? this.verificationNote,
      publishedAt: publishedAt,
      createdAt: createdAt,
      snippet: snippet,
    );
  }

  String get categoryLabel {
    switch (category) {
      case 'finance':
        return '金融/美股';
      case 'ai':
        return 'AI 行业';
      case 'world':
        return '全球突发';
      default:
        return '大厂动态';
    }
  }
}

class EventThreadMsg {
  final int id;
  final String role;
  final String contentMd;
  final String modelUsed;
  final int createdAt;

  const EventThreadMsg({
    required this.id,
    required this.role,
    required this.contentMd,
    required this.modelUsed,
    required this.createdAt,
  });

  factory EventThreadMsg.fromJson(Map<String, dynamic> j) => EventThreadMsg(
        id: (j['id'] as num?)?.toInt() ?? 0,
        role: (j['role'] as String?) ?? 'assistant',
        contentMd: (j['content_md'] as String?) ?? '',
        modelUsed: (j['model_used'] as String?) ?? '',
        createdAt: (j['created_at'] as num?)?.toInt() ?? 0,
      );
}

class DailyDigest {
  final int id;
  final String digestDate;
  final String title;
  final String contentMd;
  final int eventCount;
  final int createdAt;

  const DailyDigest({
    required this.id,
    required this.digestDate,
    required this.title,
    required this.contentMd,
    required this.eventCount,
    required this.createdAt,
  });

  factory DailyDigest.fromJson(Map<String, dynamic> j) => DailyDigest(
        id: (j['id'] as num?)?.toInt() ?? 0,
        digestDate: (j['digest_date'] as String?) ?? '',
        title: (j['title'] as String?) ?? '',
        contentMd: (j['content_md'] as String?) ?? '',
        eventCount: (j['event_count'] as num?)?.toInt() ?? 0,
        createdAt: (j['created_at'] as num?)?.toInt() ?? 0,
      );
}

class SourceHealth {
  final String sourceName;
  final String sourceType;
  final int lastSuccessAt;
  final int consecutiveFails;
  final String lastErrorMsg;

  const SourceHealth({
    required this.sourceName,
    required this.sourceType,
    required this.lastSuccessAt,
    required this.consecutiveFails,
    required this.lastErrorMsg,
  });

  factory SourceHealth.fromJson(Map<String, dynamic> j) => SourceHealth(
        sourceName: (j['source_name'] as String?) ?? '',
        sourceType: (j['source_type'] as String?) ?? '',
        lastSuccessAt: (j['last_success_at'] as num?)?.toInt() ?? 0,
        consecutiveFails: (j['consecutive_fails'] as num?)?.toInt() ?? 0,
        lastErrorMsg: (j['last_error_msg'] as String?) ?? '',
      );
}

class BudgetOverview {
  final String monthKey;
  final double totalUsd;
  final double monthlyCapUsd;
  final bool tripped;

  const BudgetOverview({
    required this.monthKey,
    required this.totalUsd,
    required this.monthlyCapUsd,
    required this.tripped,
  });

  factory BudgetOverview.fromJson(Map<String, dynamic> j) => BudgetOverview(
        monthKey: (j['month_key'] as String?) ?? '',
        totalUsd: (j['total_usd'] as num?)?.toDouble() ?? 0,
        monthlyCapUsd: (j['monthly_cap_usd'] as num?)?.toDouble() ?? 2.8,
        tripped: (j['tripped'] as bool?) ?? false,
      );
}

class SystemOverview {
  final int totalEvents;
  final int p1Events24h;
  final int bookmarkedEvents;
  final int totalDigests;
  final int dbSizeBytes;
  final int activeWsClients;
  final List<SourceHealth> sources;
  final BudgetOverview budget;

  const SystemOverview({
    required this.totalEvents,
    required this.p1Events24h,
    required this.bookmarkedEvents,
    required this.totalDigests,
    required this.dbSizeBytes,
    required this.activeWsClients,
    required this.sources,
    required this.budget,
  });

  factory SystemOverview.fromJson(Map<String, dynamic> j) => SystemOverview(
        totalEvents: (j['total_events'] as num?)?.toInt() ?? 0,
        p1Events24h: (j['p1_events_24h'] as num?)?.toInt() ?? 0,
        bookmarkedEvents: (j['bookmarked_events'] as num?)?.toInt() ?? 0,
        totalDigests: (j['total_digests'] as num?)?.toInt() ?? 0,
        dbSizeBytes: (j['db_size_bytes'] as num?)?.toInt() ?? 0,
        activeWsClients: (j['active_ws_clients'] as num?)?.toInt() ?? 0,
        sources: ((j['sources'] as List?) ?? const [])
            .map((e) => SourceHealth.fromJson(e as Map<String, dynamic>))
            .toList(),
        budget: BudgetOverview.fromJson((j['budget'] as Map<String, dynamic>?) ?? const {}),
      );
}

class ModelSelection {
  final String provider;
  final String fastModel;
  final String fallbackModel;
  final String strongModel;

  const ModelSelection({
    required this.provider,
    required this.fastModel,
    required this.fallbackModel,
    required this.strongModel,
  });

  factory ModelSelection.fromJson(Map<String, dynamic> j) => ModelSelection(
        provider: (j['provider'] as String?) ?? 'opencode-go',
        fastModel: (j['fast_model'] as String?) ?? '',
        fallbackModel: (j['fallback_model'] as String?) ?? '',
        strongModel: (j['strong_model'] as String?) ?? '',
      );

  Map<String, dynamic> toJson() => {
        'provider': provider,
        'fast_model': fastModel,
        'fallback_model': fallbackModel,
        'strong_model': strongModel,
      };
}

class ModelProviderCatalog {
  final String name;
  final String baseUrl;
  final List<String> models;

  const ModelProviderCatalog({
    required this.name,
    required this.baseUrl,
    required this.models,
  });

  factory ModelProviderCatalog.fromJson(Map<String, dynamic> j) => ModelProviderCatalog(
        name: (j['name'] as String?) ?? '',
        baseUrl: (j['base_url'] as String?) ?? '',
        models: ((j['models'] as List?) ?? const []).map((e) => e.toString()).toList(),
      );
}

class ModelSettingsResponse {
  final ModelSelection active;
  final List<ModelProviderCatalog> providers;

  const ModelSettingsResponse({
    required this.active,
    required this.providers,
  });

  factory ModelSettingsResponse.fromJson(Map<String, dynamic> j) => ModelSettingsResponse(
        active: ModelSelection.fromJson((j['active'] as Map<String, dynamic>?) ?? const {}),
        providers: ((j['providers'] as List?) ?? const [])
            .map((e) => ModelProviderCatalog.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}
