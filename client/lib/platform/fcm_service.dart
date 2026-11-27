import 'dart:async';
import 'dart:io';

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';

/// Top-level background handler required by firebase_messaging when the app is terminated/backgrounded.
@pragma('vm:entry-point')
Future<void> intelFcmBackgroundHandler(RemoteMessage message) async {
  try {
    if (Firebase.apps.isEmpty) {
      await Firebase.initializeApp();
    }
  } catch (_) {}
  debugPrint('[FCM:bg] received event_code=${message.data['event_code']}');
}

/// Crash-proof Android FCM integration:
/// - Safely no-ops on Windows or open-source builds compiled without google-services.json
/// - Handles token fetch/refresh, cold-start notification launch, and background tap deep-linking.
class FcmService {
  FcmService._();
  static final FcmService instance = FcmService._();

  bool _initialized = false;
  bool _available = false;
  String _token = '';
  String _statusNote = '未初始化';
  StreamSubscription<String>? _tokenSub;
  StreamSubscription<RemoteMessage>? _openSub;
  StreamSubscription<RemoteMessage>? _fgSub;

  bool get isAvailable => _available;
  String get token => _token;
  String get statusNote => _statusNote;

  Future<String?> init({
    required void Function(String token) onToken,
    required void Function(String eventCode) onEventTap,
    void Function(RemoteMessage msg)? onForegroundMessage,
  }) async {
    if (!Platform.isAndroid) {
      _statusNote = '仅 Android 端使用 FCM（Windows 走托盘长连接 + WinRT）';
      return null;
    }
    if (_initialized) {
      return _token.isNotEmpty ? _token : null;
    }
    _initialized = true;

    try {
      if (Firebase.apps.isEmpty) {
        await Firebase.initializeApp();
      }
      FirebaseMessaging.onBackgroundMessage(intelFcmBackgroundHandler);

      final messaging = FirebaseMessaging.instance;
      await messaging.requestPermission(
        alert: true,
        badge: true,
        sound: true,
        criticalAlert: true,
      );

      final tok = await messaging.getToken();
      if (tok != null && tok.isNotEmpty) {
        _token = tok;
        _available = true;
        _statusNote = 'FCM 已就绪（息屏零耗电推送已激活）';
        onToken(tok);
      } else {
        _available = false;
        _statusNote = '等待 Google Play 服务分配 Token（请检查网络）';
      }

      _tokenSub = messaging.onTokenRefresh.listen((newTok) {
        if (newTok.isNotEmpty) {
          _token = newTok;
          _available = true;
          _statusNote = 'FCM 已就绪（Token 已自动刷新）';
          onToken(newTok);
        }
      });

      // Cold-start deep-link when app was launched by tapping a lock-screen FCM alert
      final initialMsg = await messaging.getInitialMessage();
      final initialCode = initialMsg?.data['event_code']?.toString() ?? '';
      if (initialCode.isNotEmpty && initialCode != 'EVT-FCM-TEST') {
        Future.delayed(const Duration(milliseconds: 500), () => onEventTap(initialCode));
      }

      // Warm-start deep-link when app was in background and user tapped an FCM alert
      _openSub = FirebaseMessaging.onMessageOpenedApp.listen((msg) {
        final code = msg.data['event_code']?.toString() ?? '';
        if (code.isNotEmpty && code != 'EVT-FCM-TEST') {
          onEventTap(code);
        }
      });

      // Foreground FCM messages (useful for EVT-FCM-TEST verification)
      _fgSub = FirebaseMessaging.onMessage.listen((msg) {
        onForegroundMessage?.call(msg);
      });

      return _token.isNotEmpty ? _token : null;
    } catch (e) {
      _available = false;
      _statusNote = '未启用 FCM（当前安装包未内置 google-services.json 或无 GMS）';
      debugPrint('[FcmService] init skipped/error: $e');
      return null;
    }
  }

  Future<void> dispose() async {
    await _tokenSub?.cancel();
    await _openSub?.cancel();
    await _fgSub?.cancel();
  }
}
