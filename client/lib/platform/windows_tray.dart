import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:tray_manager/tray_manager.dart';
import 'package:window_manager/window_manager.dart';

/// Windows System Tray controller so clicking Close minimizes to tray and keeps
/// WebSocket + native WinRT Toast breaking alerts alive 24/7.
class WindowsTrayController with TrayListener {
  WindowsTrayController._();
  static final WindowsTrayController instance = WindowsTrayController._();

  bool _initialized = false;
  VoidCallback? onTestNotification;
  VoidCallback? onForceQuit;

  Future<void> init({
    VoidCallback? onTest,
    VoidCallback? onQuit,
  }) async {
    if (!Platform.isWindows || _initialized) return;
    onTestNotification = onTest;
    onForceQuit = onQuit;
    try {
      await windowManager.setPreventClose(true);
      trayManager.addListener(this);
      // Resolve bundled .ico path next to runner exe or in assets
      final exeDir = File(Platform.resolvedExecutable).parent.path;
      final candidates = [
        '$exeDir\\data\\flutter_assets\\assets\\app_icon.ico',
        'assets/app_icon.ico',
      ];
      String iconPath = candidates.last;
      for (final c in candidates) {
        if (File(c).existsSync()) {
          iconPath = c;
          break;
        }
      }
      await trayManager.setIcon(iconPath);
      await trayManager.setToolTip('informationHub · 实时运行中');
      await trayManager.setContextMenu(Menu(items: [
        MenuItem(key: 'show', label: '打开 informationHub'),
        MenuItem.separator(),
        MenuItem(key: 'test_notify', label: '发送测试原生通知'),
        MenuItem.separator(),
        MenuItem(key: 'quit', label: '彻底退出 informationHub'),
      ]));
      _initialized = true;
    } catch (e) {
      debugPrint('[WindowsTray] init warning: $e');
    }
  }

  Future<void> showMainWindow() async {
    if (!Platform.isWindows) return;
    if (await windowManager.isMinimized()) {
      await windowManager.restore();
    }
    await windowManager.show();
    await windowManager.focus();
  }

  Future<void> hideToTray() async {
    if (!Platform.isWindows) return;
    await windowManager.hide();
  }

  Future<void> forceQuit() async {
    if (!Platform.isWindows) return;
    await windowManager.setPreventClose(false);
    await trayManager.destroy();
    await windowManager.close();
  }

  @override
  void onTrayIconMouseDown() {
    showMainWindow();
  }

  @override
  void onTrayIconRightMouseDown() {
    trayManager.popUpContextMenu();
  }

  @override
  void onTrayMenuItemClick(MenuItem menuItem) {
    switch (menuItem.key) {
      case 'show':
        showMainWindow();
        break;
      case 'test_notify':
        onTestNotification?.call();
        break;
      case 'quit':
        onForceQuit?.call();
        forceQuit();
        break;
    }
  }
}
