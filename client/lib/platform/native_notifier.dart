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

  Future<double> setAndroidHighRefreshRate(bool enabled) async {
    if (!Platform.isAndroid) return 60.0;
    try {
      final res = await _nativeChannel.invokeMethod<num>('setHighRefreshRate', {'enabled': enabled});
      return res?.toDouble() ?? 60.0;
    } catch (e) {
      debugPrint('[NativeNotifier] setHighRefreshRate error: $e');
      return 60.0;
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

  Future<void> openAndroidBatterySettings() async {
    if (!Platform.isAndroid) return;
    try {
      await _nativeChannel.invokeMethod('openBatterySettings');
    } catch (e) {
      debugPrint('[NativeNotifier] openBatterySettings error: $e');
    }
  }

  /// Checks whether current local time falls inside quietHours ("HH:mm-HH:mm").
  static bool isInQuietHours(String range, {DateTime? now}) {
    final parts = range.trim().split('-');
    if (parts.length != 2) return false;
    int? toMin(String hhmm) {
      final p = hhmm.trim().split(':');
      if (p.length != 2) return null;
      final h = int.tryParse(p[0]);
      final m = int.tryParse(p[1]);
      if (h == null || m == null) return null;
      return h * 60 + m;
    }

    final start = toMin(parts[0]);
    final end = toMin(parts[1]);
    if (start == null || end == null || start == end) return false;
    final dt = now ?? DateTime.now();
    final cur = dt.hour * 60 + dt.minute;
    if (start < end) {
      return cur >= start && cur < end;
    }
    // Crosses midnight, e.g. 00:30-07:30 or 23:30-07:30
    return cur >= start || cur < end;
  }

  /// Coalesced burst notification when multiple breaking alerts arrived while offline/asleep.
  Future<void> showBurstSummary(List<IntelEvent> events) async {
    if (events.isEmpty) return;
    if (events.length == 1) {
      await showBreakingAlert(events.first);
      return;
    }
    final top = events.reduce((a, b) => a.score >= b.score ? a : b);
    final summaryEv = IntelEvent(
      id: top.id,
      eventCode: top.eventCode,
      sourceName: top.sourceName,
      category: top.category,
      rawTitle: top.rawTitle,
      zhTitle: '离线期间共 ${events.length} 条突发 · 最高 ${top.score}分：${top.zhTitle}',
      summary: top.summary,
      impactAnalysis: top.impactAnalysis.isNotEmpty ? top.impactAnalysis : '点击查看最新突发情报流',
      canonicalUrl: top.canonicalUrl,
      entities: top.entities,
      quoteSnapshot: top.quoteSnapshot,
      score: top.score,
      tier: top.tier,
      isBookmarked: top.isBookmarked,
      userNote: top.userNote,
      pushStatus: top.pushStatus,
      verificationStatus: top.verificationStatus,
      verificationNote: top.verificationNote,
      publishedAt: top.publishedAt,
      createdAt: top.createdAt,
    );
    await showBreakingAlert(summaryEv);
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
