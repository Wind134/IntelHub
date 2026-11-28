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

/// Crash-proof Android FCM integration with retryable token acquisition & clear ColorOS diagnostics:
/// - Safely no-ops on Windows or open-source builds compiled without google-services.json
/// - Retries getToken() on demand whenever network/proxy/GMS becomes ready
/// - Surfaces actionable Chinese diagnostics when GMS or Google connectivity is missing.
class FcmService {
  FcmService._();
  static final FcmService instance = FcmService._();

  bool _firebaseReady = false;
  bool _listenersAttached = false;
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

    if (!_firebaseReady) {
      try {
        if (Firebase.apps.isEmpty) {
          await Firebase.initializeApp();
        }
        FirebaseMessaging.onBackgroundMessage(intelFcmBackgroundHandler);
        _firebaseReady = true;
      } catch (e) {
        _available = false;
        _statusNote = _diagnoseError(e, stage: 'init');
        debugPrint('[FcmService] Firebase.initializeApp failed: $e');
        return null;
      }
    }

    final messaging = FirebaseMessaging.instance;
    if (!_listenersAttached) {
      _listenersAttached = true;
      try {
        await messaging.requestPermission(
          alert: true,
          badge: true,
          sound: true,
          criticalAlert: true,
        );
      } catch (_) {}

      _tokenSub = messaging.onTokenRefresh.listen((newTok) {
        if (newTok.isNotEmpty) {
          _token = newTok;
          _available = true;
          _statusNote = 'FCM 已就绪（Token 已自动刷新）';
          onToken(newTok);
        }
      });

      final initialMsg = await messaging.getInitialMessage();
      final initialCode = initialMsg?.data['event_code']?.toString() ?? '';
      if (initialCode.isNotEmpty && initialCode != 'EVT-FCM-TEST') {
        Future.delayed(const Duration(milliseconds: 500), () => onEventTap(initialCode));
      }

      _openSub = FirebaseMessaging.onMessageOpenedApp.listen((msg) {
        final code = msg.data['event_code']?.toString() ?? '';
        if (code.isNotEmpty && code != 'EVT-FCM-TEST') {
          onEventTap(code);
        }
      });

      _fgSub = FirebaseMessaging.onMessage.listen((msg) {
        onForegroundMessage?.call(msg);
      });
    }

    return fetchOrRefreshToken(onToken: onToken);
  }

  /// Retryable token fetch (can be called again after enabling proxy or Google Play Services on ColorOS).
  Future<String?> fetchOrRefreshToken({
    required void Function(String token) onToken,
  }) async {
    if (!Platform.isAndroid) return null;
    if (!_firebaseReady) {
      try {
        if (Firebase.apps.isEmpty) {
          await Firebase.initializeApp();
        }
        _firebaseReady = true;
      } catch (e) {
        _available = false;
        _statusNote = _diagnoseError(e, stage: 'init');
        return null;
      }
    }

    try {
      _statusNote = '正在向 Google FCM 申请设备 Token…';
      final tok = await FirebaseMessaging.instance
          .getToken()
          .timeout(const Duration(seconds: 12));
      if (tok != null && tok.isNotEmpty) {
        _token = tok;
        _available = true;
        _statusNote = 'FCM 已就绪（息屏零耗电推送已激活）';
        onToken(tok);
        return tok;
      }
      _available = false;
      _statusNote = 'Google 未返回 Token：请确认已开启 ColorOS 谷歌基础服务及可访问 Google 网络后重试';
      return null;
    } catch (e) {
      _available = false;
      _statusNote = _diagnoseError(e, stage: 'token');
      debugPrint('[FcmService] getToken error: $e');
      return null;
    }
  }

  String _diagnoseError(Object e, {required String stage}) {
    final msg = e.toString();
    final lower = msg.toLowerCase();
    if (lower.contains('service_not_available') ||
        lower.contains('timeout') ||
        lower.contains('ioexception') ||
        lower.contains('fis_auth_error') ||
        lower.contains('network')) {
      return '无法连接 Google FCM 注册服务器（国内网络注册 Token 时需开启代理，开启后点“重新获取 Token”）';
    }
    if (lower.contains('missing_instanceid_service') ||
        lower.contains('google play services') ||
        lower.contains('gms')) {
      return '未检测到 Google 基础服务：请到 ColorOS「设置 → 系统与更新/应用 → Google 基础服务」开启后重试';
    }
    if (stage == 'init' && (lower.contains('default firebaseapp') || lower.contains('firebaseoptions'))) {
      return '当前 APK 未内置 google-services.json（请安装带 FCM 配置的正式包）';
    }
    final short = msg.length > 90 ? '${msg.substring(0, 90)}…' : msg;
    return 'FCM 获取失败：$short';
  }

  Future<void> dispose() async {
    await _tokenSub?.cancel();
    await _openSub?.cancel();
    await _fgSub?.cancel();
  }
}
