import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:local_notifier/local_notifier.dart';

import '../core/models.dart';

/// Unified native OS notification & ColorOS 17 background keep-alive bridge:
/// - Windows: WinRT Native Toast Notifications (with sound & click callback)
/// - Android / ColorOS 17: High/Max-priority Notification Channels (Heads-Up + Lock-screen visibility)
///   plus optional Foreground DataSync Service so ColorOS keeps the live WebSocket alive in background.
class NativeNotifier {
  NativeNotifier._();
  static final NativeNotifier instance = NativeNotifier._();

  static const MethodChannel _nativeChannel = MethodChannel('io.intelhub.client/native');
  final FlutterLocalNotificationsPlugin _androidPlugin = FlutterLocalNotificationsPlugin();
  bool _initialized = false;
  void Function(String eventCode)? onNotificationTap;

  Future<void> init({void Function(String eventCode)? onTap}) async {
    onNotificationTap = onTap;
    if (_initialized) return;

    try {
      if (Platform.isWindows) {
        await localNotifier.setup(
          appName: 'IntelHub 突发情报台',
          shortcutPolicy: ShortcutPolicy.requireCreate,
        );
        _initialized = true;
      } else if (Platform.isAndroid) {
        // IMPORTANT: flutter_local_notifications expects bare drawable name WITHOUT "@drawable/" prefix!
        const initSettings = InitializationSettings(
          android: AndroidInitializationSettings('ic_stat_intel'),
        );
        await _androidPlugin.initialize(
          initSettings,
          onDidReceiveNotificationResponse: (resp) {
            final code = resp.payload;
            if (code != null && code.isNotEmpty) {
              onNotificationTap?.call(code);
            }
          },
        );
        final androidImpl = _androidPlugin
            .resolvePlatformSpecificImplementation<AndroidFlutterLocalNotificationsPlugin>();
        if (androidImpl != null) {
          await androidImpl.createNotificationChannel(const AndroidNotificationChannel(
            'intel_critical',
            '特急突发红警 (9-10分)',
            description: '重大黑天鹅、核心标的剧烈异动、旗舰模型发布与大面积宕机',
            importance: Importance.max,
            playSound: true,
            enableVibration: true,
            enableLights: true,
            showBadge: true,
          ));
          await androidImpl.createNotificationChannel(const AndroidNotificationChannel(
            'intel_high',
            '重要突发快讯 (8分)',
            description: '高价值科技与金融突发事件提醒',
            importance: Importance.high,
            playSound: true,
            enableVibration: true,
            showBadge: true,
          ));
          // Request Android 13+ / ColorOS 17 runtime notification permission safely
          await androidImpl.requestNotificationsPermission();
        }
        _initialized = true;
      }
    } catch (e) {
      debugPrint('[NativeNotifier] init warning: $e');
    }
  }

  Future<void> startAndroidKeepAlive(String statusText) async {
    if (!Platform.isAndroid) return;
    try {
      await _nativeChannel.invokeMethod('startKeepAlive', {'text': statusText});
    } catch (e) {
      debugPrint('[NativeNotifier] startKeepAlive error: $e');
    }
  }

  Future<void> stopAndroidKeepAlive() async {
    if (!Platform.isAndroid) return;
    try {
      await _nativeChannel.invokeMethod('stopKeepAlive');
    } catch (e) {
      debugPrint('[NativeNotifier] stopKeepAlive error: $e');
    }
  }

  Future<void> openAndroidNotificationSettings() async {
    if (!Platform.isAndroid) return;
    try {
      await _nativeChannel.invokeMethod('openNotificationSettings');
    } catch (e) {
      debugPrint('[NativeNotifier] openNotificationSettings error: $e');
    }
  }

  Future<void> showBreakingAlert(IntelEvent ev) async {
    if (!_initialized) {
      await init(onTap: onNotificationTap);
    }
    final prefix = ev.score >= 9 ? '🔴 特急' : '🟠 突发';
    final title = '$prefix [${ev.score}/10 ${ev.categoryLabel}] ${ev.zhTitle}';
    final body = ev.impactAnalysis.isNotEmpty ? ev.impactAnalysis : ev.summary;

    try {
      if (Platform.isWindows) {
        final n = LocalNotification(
          identifier: ev.eventCode,
          title: title,
          body: body,
        );
        n.onClick = () => onNotificationTap?.call(ev.eventCode);
        await n.show();
      } else if (Platform.isAndroid) {
        final isCritical = ev.score >= 9;
        final details = NotificationDetails(
          android: AndroidNotificationDetails(
            isCritical ? 'intel_critical' : 'intel_high',
            isCritical ? '特急突发红警 (9-10分)' : '重要突发快讯 (8分)',
            icon: 'ic_stat_intel',
            importance: isCritical ? Importance.max : Importance.high,
            priority: isCritical ? Priority.max : Priority.high,
            visibility: NotificationVisibility.public,
            styleInformation: BigTextStyleInformation(body),
            category: isCritical ? AndroidNotificationCategory.alarm : AndroidNotificationCategory.message,
            ticker: title,
          ),
        );
        await _androidPlugin.show(
          ev.id.remainder(100000),
          title,
          body,
          details,
          payload: ev.eventCode,
        );
      }
    } catch (e) {
      debugPrint('[NativeNotifier] showBreakingAlert error: $e');
    }
  }
}
