import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:web_socket_channel/web_socket_channel.dart';

import 'models.dart';

class IntelApiClient {
  String baseUrl;
  String apiToken;
  final http.Client _http = http.Client();

  IntelApiClient({required this.baseUrl, required this.apiToken});

  String get _cleanBase => baseUrl.trim().replaceAll(RegExp(r'/+$'), '');

  Map<String, String> get _headers => {
        'Content-Type': 'application/json',
        if (apiToken.trim().isNotEmpty) 'Authorization': 'Bearer ${apiToken.trim()}',
      };

  Future<bool> checkHealth() async {
    if (_cleanBase.isEmpty) return false;
    final resp = await _http.get(Uri.parse('$_cleanBase/api/v1/health')).timeout(const Duration(seconds: 6));
    return resp.statusCode == 200;
  }

  Future<List<IntelEvent>> fetchEvents({
    String? query,
    List<String>? categories,
    int? minScore,
    int? maxScore,
    int? fromTs,
    int? toTs,
    String? entity,
    bool bookmarkedOnly = false,
    String sort = 'time_desc',
    int? beforeId,
    int limit = 50,
  }) async {
    final params = <String, String>{
      'limit': '$limit',
      'sort': sort,
    };
    if (query != null && query.trim().isNotEmpty) params['q'] = query.trim();
    if (categories != null && categories.isNotEmpty) params['categories'] = categories.join(',');
    if (minScore != null && minScore > 0) params['min_score'] = '$minScore';
    if (maxScore != null && maxScore > 0) params['max_score'] = '$maxScore';
    if (fromTs != null && fromTs > 0) params['from_ts'] = '$fromTs';
    if (toTs != null && toTs > 0) params['to_ts'] = '$toTs';
    if (entity != null && entity.trim().isNotEmpty) params['entity'] = entity.trim();
    if (bookmarkedOnly) params['bookmarked_only'] = '1';
    if (beforeId != null && beforeId > 0) params['before_id'] = '$beforeId';

    final path = (query != null && query.trim().isNotEmpty) ? '/api/v1/events/search' : '/api/v1/events';
    final uri = Uri.parse('$_cleanBase$path').replace(queryParameters: params);
    final resp = await _http.get(uri, headers: _headers).timeout(const Duration(seconds: 12));
    if (resp.statusCode != 200) {
      throw HttpException('HTTP ${resp.statusCode}: ${resp.body}');
    }
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    final list = (data['items'] as List?) ?? const [];
    return list.map((e) => IntelEvent.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<({IntelEvent event, List<EventThreadMsg> threads, List<IntelEvent> related})> fetchEventDetail(
      String code) async {
    final resp = await _http
        .get(Uri.parse('$_cleanBase/api/v1/events/$code'), headers: _headers)
        .timeout(const Duration(seconds: 12));
    if (resp.statusCode != 200) {
      throw HttpException('HTTP ${resp.statusCode}');
    }
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    return (
      event: IntelEvent.fromJson(data['event'] as Map<String, dynamic>),
      threads: ((data['threads'] as List?) ?? const [])
          .map((e) => EventThreadMsg.fromJson(e as Map<String, dynamic>))
          .toList(),
      related: ((data['related'] as List?) ?? const [])
          .map((e) => IntelEvent.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }

  Future<List<IntelEvent>> fetchEntityTimeline(String entity, {int? fromTs, int? toTs}) async {
    final params = <String, String>{'limit': '80'};
    if (fromTs != null && fromTs > 0) params['from_ts'] = '$fromTs';
    if (toTs != null && toTs > 0) params['to_ts'] = '$toTs';
    final uri = Uri.parse('$_cleanBase/api/v1/entities/${Uri.encodeComponent(entity)}/timeline')
        .replace(queryParameters: params);
    final resp = await _http.get(uri, headers: _headers).timeout(const Duration(seconds: 12));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}');
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    return ((data['items'] as List?) ?? const [])
        .map((e) => IntelEvent.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<IntelEvent> setBookmark(String code, {required bool bookmarked, String? userNote}) async {
    final body = <String, dynamic>{'is_bookmarked': bookmarked};
    if (userNote != null) body['user_note'] = userNote;
    final resp = await _http
        .patch(Uri.parse('$_cleanBase/api/v1/events/$code/bookmark'), headers: _headers, body: jsonEncode(body))
        .timeout(const Duration(seconds: 10));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}');
    return IntelEvent.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);
  }

  Future<List<DailyDigest>> fetchDigests({String? month}) async {
    final params = <String, String>{'limit': '60'};
    if (month != null && month.isNotEmpty) params['month'] = month;
    final uri = Uri.parse('$_cleanBase/api/v1/digests').replace(queryParameters: params);
    final resp = await _http.get(uri, headers: _headers).timeout(const Duration(seconds: 12));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}');
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    return ((data['items'] as List?) ?? const [])
        .map((e) => DailyDigest.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<SystemOverview> fetchOverview() async {
    final resp = await _http
        .get(Uri.parse('$_cleanBase/api/v1/system/overview'), headers: _headers)
        .timeout(const Duration(seconds: 10));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}');
    return SystemOverview.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);
  }

  Future<ModelSettingsResponse> fetchModelSettings() async {
    final resp = await _http
        .get(Uri.parse('$_cleanBase/api/v1/settings/models'), headers: _headers)
        .timeout(const Duration(seconds: 10));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}');
    return ModelSettingsResponse.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);
  }

  Future<ModelSettingsResponse> updateModelSettings(ModelSelection sel) async {
    final resp = await _http
        .patch(
          Uri.parse('$_cleanBase/api/v1/settings/models'),
          headers: _headers,
          body: jsonEncode(sel.toJson()),
        )
        .timeout(const Duration(seconds: 10));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}: ${resp.body}');
    return ModelSettingsResponse.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);
  }

  Future<void> syncDevice({
    required String deviceId,
    required String deviceName,
    required String platform,
    required int minPushScore,
    String fcmToken = '',
  }) async {
    await _http.post(
      Uri.parse('$_cleanBase/api/v1/devices/sync'),
      headers: _headers,
      body: jsonEncode({
        'device_id': deviceId,
        'device_name': deviceName,
        'platform': platform,
        'min_push_score': minPushScore,
        'fcm_token': fcmToken,
      }),
    );
  }

  /// Streams SSE token deltas for single-event deep dive or cross-history RAG.
  Stream<String> streamAsk({
    required String endpointPath,
    required Map<String, dynamic> payload,
  }) async* {
    final req = http.Request('POST', Uri.parse('$_cleanBase$endpointPath'));
    req.headers.addAll(_headers);
    req.body = jsonEncode(payload);
    final resp = await _http.send(req);
    if (resp.statusCode != 200) {
      throw HttpException('SSE HTTP ${resp.statusCode}');
    }
    await for (final line in resp.stream.transform(utf8.decoder).transform(const LineSplitter())) {
      if (!line.startsWith('data:')) continue;
      final raw = line.substring(5).trim();
      if (raw.isEmpty) continue;
      try {
        final obj = jsonDecode(raw) as Map<String, dynamic>;
        if (obj['delta'] is String) {
          yield obj['delta'] as String;
        }
        if (obj['error'] is String) {
          yield '\n\n⚠️ ${obj['error']}';
        }
      } catch (_) {}
    }
  }

  /// Opens a realtime WebSocket connection to /api/v1/stream with cursor replay.
  WebSocketChannel connectStream({int afterId = 0}) {
    var wsBase = _cleanBase;
    if (wsBase.startsWith('https://')) {
      wsBase = 'wss://${wsBase.substring(8)}';
    } else if (wsBase.startsWith('http://')) {
      wsBase = 'ws://${wsBase.substring(7)}';
    }
    final params = <String, String>{
      if (afterId > 0) 'after_id': '$afterId',
      if (apiToken.trim().isNotEmpty) 'token': apiToken.trim(),
    };
    final uri = Uri.parse('$wsBase/api/v1/stream').replace(queryParameters: params);
    return WebSocketChannel.connect(uri);
  }
}
