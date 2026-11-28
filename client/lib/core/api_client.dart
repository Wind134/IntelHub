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
    int? afterId,
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
    if (afterId != null && afterId > 0) params['after_id'] = '$afterId';

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

  Future<DailyDigest> generateTodayDigest() async {
    final resp = await _http
        .post(Uri.parse('$_cleanBase/api/v1/digests/generate'), headers: _headers)
        .timeout(const Duration(seconds: 120));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}: ${resp.body}');
    return DailyDigest.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);
  }

  Future<NotificationPolicy> fetchNotificationPolicy() async {
    final resp = await _http
        .get(Uri.parse('$_cleanBase/api/v1/settings/notifications'), headers: _headers)
        .timeout(const Duration(seconds: 10));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}');
    return NotificationPolicy.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);
  }

  Future<NotificationPolicy> updateNotificationPolicy(NotificationPolicy policy) async {
    final resp = await _http
        .patch(
          Uri.parse('$_cleanBase/api/v1/settings/notifications'),
          headers: _headers,
          body: jsonEncode(policy.toJson()),
        )
        .timeout(const Duration(seconds: 10));
    if (resp.statusCode != 200) throw HttpException('HTTP ${resp.statusCode}: ${resp.body}');
    return NotificationPolicy.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);
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

  Future<bool> syncDevice({
    required String deviceId,
    required String deviceName,
    required String platform,
    required int minPushScore,
    List<String> categories = const ['finance', 'ai', 'bigtech', 'world'],
    String quietHours = '',
    String fcmToken = '',
  }) async {
    final resp = await _http
        .post(
          Uri.parse('$_cleanBase/api/v1/devices/sync'),
          headers: _headers,
          body: jsonEncode({
            'device_id': deviceId,
            'device_name': deviceName,
            'platform': platform,
            'min_push_score': minPushScore,
            'categories': categories,
            'quiet_hours': quietHours,
            'fcm_token': fcmToken,
          }),
        )
        .timeout(const Duration(seconds: 10));
    if (resp.statusCode != 200) {
      throw HttpException('HTTP ${resp.statusCode}: ${resp.body}');
    }
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    return data['fcm_enabled'] == true;
  }

  Future<Map<String, dynamic>> testFcmPush({String deviceId = '', int delaySec = 5}) async {
    final resp = await _http
        .post(
          Uri.parse('$_cleanBase/api/v1/devices/test-fcm'),
          headers: _headers,
          body: jsonEncode({
            'device_id': deviceId,
            'delay_sec': delaySec,
          }),
        )
        .timeout(const Duration(seconds: 15));
    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    if (resp.statusCode != 200) {
      throw HttpException(data['error']?.toString() ?? 'HTTP ${resp.statusCode}');
    }
    return data;
  }

  /// Streams SSE token deltas using a dedicated fresh HTTP client per request (eliminates
  /// stale keep-alive socket drops after sleep / Wi-Fi↔5G switch) with automatic 1-time retry.
  Stream<String> streamAsk({
    required String endpointPath,
    required Map<String, dynamic> payload,
    void Function(String status)? onStatus,
  }) async* {
    const maxAttempts = 2;
    for (var attempt = 1; attempt <= maxAttempts; attempt++) {
      final streamClient = http.Client();
      var yieldedAny = false;
      try {
        final req = http.Request('POST', Uri.parse('$_cleanBase$endpointPath'));
        req.headers.addAll({
          ..._headers,
          'Accept': 'text/event-stream',
          'Cache-Control': 'no-cache',
          'Connection': 'close',
        });
        req.body = jsonEncode(payload);

        final resp = await streamClient.send(req).timeout(const Duration(seconds: 30));
        if (resp.statusCode != 200) {
          final errBody = await resp.stream.bytesToString().timeout(const Duration(seconds: 5), onTimeout: () => '');
          throw HttpException('SSE HTTP ${resp.statusCode}${errBody.isNotEmpty ? ": $errBody" : ""}');
        }
        await for (final line in resp.stream.transform(utf8.decoder).transform(const LineSplitter())) {
          if (!line.startsWith('data:')) continue;
          final raw = line.substring(5).trim();
          if (raw.isEmpty) continue;
          try {
            final obj = jsonDecode(raw) as Map<String, dynamic>;
            if (obj['status'] is String) {
              onStatus?.call(obj['status'] as String);
            }
            if (obj['delta'] is String) {
              yieldedAny = true;
              yield obj['delta'] as String;
            }
            if (obj['error'] is String) {
              yieldedAny = true;
              yield '\n\n⚠️ ${obj['error']}';
            }
          } catch (_) {}
        }
        return;
      } catch (e) {
        if (yieldedAny || attempt >= maxAttempts) {
          rethrow;
        }
        onStatus?.call('连接波动，正在自动重连重试…');
        await Future.delayed(const Duration(milliseconds: 1100));
      } finally {
        streamClient.close();
      }
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
