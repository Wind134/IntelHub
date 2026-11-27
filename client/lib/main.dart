import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:window_manager/window_manager.dart';

import 'core/api_client.dart';
import 'core/app_theme.dart';
import 'core/markdown_view.dart';
import 'core/models.dart';
import 'core/updater.dart';
import 'platform/native_notifier.dart';
import 'platform/windows_tray.dart';

// Cached static DateFormat instances to eliminate per-frame GC allocations during 120Hz scrolling.
final DateFormat _fmtShort = DateFormat('MM-dd HH:mm');
final DateFormat _fmtFull = DateFormat('yyyy-MM-dd HH:mm');
final DateFormat _fmtMonthDay = DateFormat('MM/dd');

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  if (Platform.isWindows) {
    await windowManager.ensureInitialized();
    const opts = WindowOptions(
      size: Size(1320, 840),
      minimumSize: Size(920, 620),
      center: true,
      title: 'IntelHub · 全天候突发情报终端',
      titleBarStyle: TitleBarStyle.hidden,
      backgroundColor: Colors.transparent,
    );
    windowManager.waitUntilReadyToShow(opts, () async {
      await windowManager.show();
      await windowManager.focus();
    });
  }
  runApp(const IntelHubApp());
}

class IntelHubApp extends StatefulWidget {
  const IntelHubApp({super.key});

  @override
  State<IntelHubApp> createState() => _IntelHubAppState();
}

class _IntelHubAppState extends State<IntelHubApp> {
  ThemeMode _themeMode = ThemeMode.system;

  @override
  void initState() {
    super.initState();
    _loadTheme();
  }

  Future<void> _loadTheme() async {
    final prefs = await SharedPreferences.getInstance();
    final saved = prefs.getString('theme_mode') ?? 'system';
    if (!mounted) return;
    setState(() {
      _themeMode = switch (saved) {
        'light' => ThemeMode.light,
        'dark' => ThemeMode.dark,
        _ => ThemeMode.system,
      };
    });
  }

  Future<void> _setThemeMode(ThemeMode mode) async {
    setState(() => _themeMode = mode);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(
      'theme_mode',
      switch (mode) {
        ThemeMode.light => 'light',
        ThemeMode.dark => 'dark',
        ThemeMode.system => 'system',
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'IntelHub 突发情报台',
      debugShowCheckedModeBanner: false,
      themeMode: _themeMode,
      theme: AppTheme.light(),
      darkTheme: AppTheme.dark(),
      home: IntelShellPage(
        themeMode: _themeMode,
        onThemeModeChanged: _setThemeMode,
      ),
    );
  }
}

class IntelShellPage extends StatefulWidget {
  final ThemeMode themeMode;
  final ValueChanged<ThemeMode> onThemeModeChanged;

  const IntelShellPage({
    super.key,
    required this.themeMode,
    required this.onThemeModeChanged,
  });

  @override
  State<IntelShellPage> createState() => _IntelShellPageState();
}

class _IntelShellPageState extends State<IntelShellPage> with WindowListener, WidgetsBindingObserver {
  final IntelApiClient _api = IntelApiClient(baseUrl: '', apiToken: '');
  WebSocketChannel? _ws;
  StreamSubscription? _wsSub;
  Timer? _reconnectTimer;
  Timer? _fallbackPollTimer;

  bool _isMaximized = false;
  int _navIndex = 0; // 0: 实时流, 1: 历史库, 2: 每日内参, 3: 系统设置
  bool _wsConnected = false;

  // Local device push policy (persisted in SharedPreferences)
  int _minPushScore = 8;
  Set<String> _localNotifyCategories = {'finance', 'ai', 'bigtech', 'world'};
  bool _androidKeepAlive = true;
  bool _winMinimizeToTray = true;
  bool _quietHoursEnabled = false;
  String _quietHoursRange = '00:30-07:30';
  bool _criticalBypassQuiet = true;
  int _lastEventId = 0;
  final Set<int> _notifiedIds = <int>{};

  // Server-wide Notification Policy (hot-synced to backend)
  int _serverMinScore = 8;
  Set<String> _serverNotifyCategories = {'finance', 'ai', 'bigtech', 'world'};
  bool _serverEnableFeishu = true;
  bool _serverEnableVerify = true;
  bool _serverEnableDailyDigest = true;
  bool _savingNotifyPolicy = false;
  bool _generatingDigest = false;

  // Separate Live Feed vs Historical Archive lists (zero reset on tab switch via IndexedStack!)
  List<IntelEvent> _liveEvents = [];
  List<IntelEvent> _archiveEvents = [];
  bool _loadingLive = false;
  bool _loadingMoreLive = false;
  bool _hasMoreLive = true;
  bool _loadingArchive = false;
  bool _loadingMoreArchive = false;
  bool _hasMoreArchive = true;
  String? _loadError;

  // Filters: Live vs Archive
  String _liveCategory = 'all';
  int _liveMinScore = 1;

  final TextEditingController _searchCtrl = TextEditingController();
  String _archiveCategory = 'all';
  int _archiveMinScore = 1;
  bool _bookmarkedOnly = false;
  String _sortMode = 'time_desc';
  DateTimeRange? _dateRange;
  String? _activeEntity;

  // Detail pane state (isolated reactivity for zero full-tree rebuilds during AI streaming!)
  IntelEvent? _selectedEvent;
  List<EventThreadMsg> _selectedThreads = [];
  List<IntelEvent> _selectedRelated = [];
  final ValueNotifier<int> _detailTick = ValueNotifier<int>(0);
  final ValueNotifier<String> _streamNotifier = ValueNotifier<String>('');
  final ValueNotifier<bool> _askingNotifier = ValueNotifier<bool>(false);
  final TextEditingController _askCtrl = TextEditingController();
  final ScrollController _detailScrollCtrl = ScrollController();
  final ScrollController _liveScrollCtrl = ScrollController();
  final ScrollController _archiveScrollCtrl = ScrollController();
  bool _webSearchInAsk = true;

  // Digests & Overview
  List<DailyDigest> _digests = [];
  DailyDigest? _selectedDigest;
  SystemOverview? _overview;
  bool _loadedDigestsOnce = false;
  bool _loadedArchiveOnce = false;

  // Independent 3-slot Model Service Selector
  ModelSettingsResponse? _modelSettings;
  String _selectedProvider = 'opencode-go';
  String _fastModel = 'deepseek-v4.1-flash';
  String _fallbackModel = 'qwen3.8-flash';
  String _strongModel = 'deepseek-v4-pro';
  bool _savingModels = false;

  // Settings controllers
  final TextEditingController _urlCtrl = TextEditingController();
  final TextEditingController _tokenCtrl = TextEditingController();
  final TextEditingController _quietCtrl = TextEditingController();
  final TextEditingController _repoCtrl = TextEditingController();
  final TextEditingController _ghTokenCtrl = TextEditingController();
  bool _checkingUpdate = false;

  void _bumpDetail() => _detailTick.value++;

  void _scrollDetailToBottomIfNear({bool force = false}) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_detailScrollCtrl.hasClients) return;
      final pos = _detailScrollCtrl.position;
      if (force || (pos.maxScrollExtent - pos.pixels) < 180) {
        _detailScrollCtrl.animateTo(
          pos.maxScrollExtent,
          duration: const Duration(milliseconds: 220),
          curve: Curves.easeOut,
        );
      }
    });
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _liveScrollCtrl.addListener(_onLiveScroll);
    _archiveScrollCtrl.addListener(_onArchiveScroll);
    if (Platform.isWindows) {
      windowManager.addListener(this);
    }
    _bootstrap();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    if (Platform.isWindows) {
      windowManager.removeListener(this);
    }
    _wsSub?.cancel();
    _ws?.sink.close();
    _reconnectTimer?.cancel();
    _fallbackPollTimer?.cancel();
    _liveScrollCtrl.dispose();
    _archiveScrollCtrl.dispose();
    _detailScrollCtrl.dispose();
    _detailTick.dispose();
    _streamNotifier.dispose();
    _askingNotifier.dispose();
    _searchCtrl.dispose();
    _askCtrl.dispose();
    _urlCtrl.dispose();
    _tokenCtrl.dispose();
    _quietCtrl.dispose();
    _repoCtrl.dispose();
    _ghTokenCtrl.dispose();
    super.dispose();
  }

  void _onLiveScroll() {
    if (_loadingMoreLive || !_hasMoreLive || _liveEvents.isEmpty) return;
    if (_liveScrollCtrl.position.pixels >= _liveScrollCtrl.position.maxScrollExtent - 320) {
      _loadMoreLive();
    }
  }

  void _onArchiveScroll() {
    if (_loadingMoreArchive || !_hasMoreArchive || _archiveEvents.isEmpty) return;
    if (_archiveScrollCtrl.position.pixels >= _archiveScrollCtrl.position.maxScrollExtent - 320) {
      _loadMoreArchive();
    }
  }

  @override
  void onWindowMaximize() => setState(() => _isMaximized = true);

  @override
  void onWindowUnmaximize() => setState(() => _isMaximized = false);

  @override
  void onWindowClose() async {
    if (Platform.isWindows && _winMinimizeToTray) {
      await WindowsTrayController.instance.hideToTray();
    } else {
      await WindowsTrayController.instance.forceQuit();
    }
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      if (!_wsConnected && _api.baseUrl.trim().isNotEmpty) {
        _connectWebSocket();
      }
      _pollMissedAlerts();
      if (Platform.isAndroid && _androidKeepAlive && _api.baseUrl.trim().isNotEmpty) {
        NativeNotifier.instance.startAndroidKeepAlive(_wsConnected ? '实时在线 · 突发秒级监测中' : '正在重连…');
      }
    }
  }

  Future<void> _bootstrap() async {
    // 1. Load ALL persisted settings & cached feed FIRST for <50ms instant startup!
    final prefs = await SharedPreferences.getInstance();
    final savedUrl = prefs.getString('server_url') ?? '';
    final savedToken = prefs.getString('api_token') ?? '';
    final savedRepo = prefs.getString('github_repo');
    final effectiveRepo = (savedRepo != null && savedRepo.trim().isNotEmpty) ? savedRepo : kDefaultGithubRepo;
    final savedGhToken = prefs.getString('github_token') ?? '';

    _minPushScore = prefs.getInt('min_push_score') ?? 8;
    final savedLocalCats = prefs.getStringList('local_notify_categories');
    if (savedLocalCats != null && savedLocalCats.isNotEmpty) {
      _localNotifyCategories = savedLocalCats.toSet();
    }
    _androidKeepAlive = prefs.getBool('android_keep_alive') ?? true;
    _winMinimizeToTray = prefs.getBool('win_minimize_to_tray') ?? true;
    _quietHoursEnabled = prefs.getBool('quiet_hours_enabled') ?? false;
    _quietHoursRange = prefs.getString('quiet_hours_range') ?? '00:30-07:30';
    _criticalBypassQuiet = prefs.getBool('critical_bypass_quiet') ?? true;
    _lastEventId = prefs.getInt('last_seen_event_id') ?? 0;

    _api.baseUrl = savedUrl;
    _api.apiToken = savedToken;
    _urlCtrl.text = savedUrl;
    _tokenCtrl.text = savedToken;
    _quietCtrl.text = _quietHoursRange;
    _repoCtrl.text = effectiveRepo;
    _ghTokenCtrl.text = savedGhToken;

    // Instant cold-start render from cached live events JSON
    final cachedJson = prefs.getString('cached_live_events_v1');
    if (cachedJson != null && cachedJson.isNotEmpty) {
      try {
        final decoded = (jsonDecode(cachedJson) as List)
            .map((e) => IntelEvent.fromJson(e as Map<String, dynamic>))
            .toList();
        if (decoded.isNotEmpty) {
          _liveEvents = decoded;
          _selectedEvent = decoded.first;
        }
      } catch (_) {}
    }

    if (mounted) {
      setState(() {
        if (savedUrl.isEmpty) _navIndex = 3;
      });
    }

    // 2. Initialize Windows Tray + Native OS Notification channels
    if (Platform.isWindows) {
      _isMaximized = await windowManager.isMaximized();
      await WindowsTrayController.instance.init(
        onTest: _fireTestNotification,
      );
    }
    await NativeNotifier.instance.init(onTap: (code) => _openEventByCode(code));

    if (savedUrl.isNotEmpty) {
      await _refreshLiveEvents(silent: _liveEvents.isNotEmpty);
      _connectWebSocket();
      _startFallbackPoller();
      _loadModelSettings();
      _loadNotificationPolicy();
      if (Platform.isAndroid && _androidKeepAlive) {
        await NativeNotifier.instance.startAndroidKeepAlive('实时在线 · 突发秒级监测中');
      }
    }
  }

  Future<void> _persistLastEventId(int id) async {
    if (id <= _lastEventId) return;
    _lastEventId = id;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt('last_seen_event_id', id);
  }

  Future<void> _cacheLiveEvents(List<IntelEvent> list) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final slice = list.take(50).map((e) => e.toJson()).toList();
      await prefs.setString('cached_live_events_v1', jsonEncode(slice));
    } catch (_) {}
  }

  bool _shouldFireLocalAlert(IntelEvent ev) {
    if (ev.score < _minPushScore) return false;
    if (!_localNotifyCategories.contains(ev.category)) return false;
    if (ev.pushStatus == 'cooled_down') return false;
    if (_notifiedIds.contains(ev.id)) return false;
    if (_quietHoursEnabled && NativeNotifier.isInQuietHours(_quietHoursRange)) {
      if (!(_criticalBypassQuiet && ev.score >= 9)) {
        return false;
      }
    }
    return true;
  }

  void _connectWebSocket() {
    _wsSub?.cancel();
    _ws?.sink.close();
    _reconnectTimer?.cancel();
    if (_api.baseUrl.trim().isEmpty) return;

    try {
      _ws = _api.connectStream(afterId: _lastEventId);
      if (mounted) setState(() => _wsConnected = true);
      _wsSub = _ws!.stream.listen(
        (raw) {
          try {
            final msg = jsonDecode(raw as String) as Map<String, dynamic>;
            final type = msg['type'] as String? ?? '';
            if (msg['event'] is Map<String, dynamic>) {
              final ev = IntelEvent.fromJson(msg['event'] as Map<String, dynamic>);
              _handleStreamEvent(type, ev);
            }
          } catch (_) {}
        },
        onError: (_) => _scheduleReconnect(),
        onDone: () => _scheduleReconnect(),
      );
    } catch (_) {
      _scheduleReconnect();
    }
  }

  void _scheduleReconnect() {
    if (!mounted) return;
    if (_wsConnected) setState(() => _wsConnected = false);
    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(const Duration(seconds: 5), _connectWebSocket);
  }

  void _startFallbackPoller() {
    _fallbackPollTimer?.cancel();
    _fallbackPollTimer = Timer.periodic(const Duration(minutes: 2), (_) => _pollMissedAlerts());
  }

  /// Fetches any events with id > _lastEventId missed during sleep/Doze and coalesces burst notifications.
  Future<void> _pollMissedAlerts() async {
    if (_api.baseUrl.trim().isEmpty || _lastEventId <= 0) return;
    try {
      final missed = await _api.fetchEvents(afterId: _lastEventId, limit: 30);
      if (missed.isEmpty) {
        if (!_wsConnected) _connectWebSocket();
        return;
      }
      var maxId = _lastEventId;
      final toNotify = <IntelEvent>[];
      for (final ev in missed) {
        if (ev.id > maxId) maxId = ev.id;
        _upsertIntoLive(ev);
        if (_shouldFireLocalAlert(ev)) {
          _notifiedIds.add(ev.id);
          toNotify.add(ev);
        }
      }
      await _persistLastEventId(maxId);
      if (mounted) setState(() {});
      if (toNotify.isNotEmpty) {
        await NativeNotifier.instance.showBurstSummary(toNotify);
      }
      if (!_wsConnected) _connectWebSocket();
    } catch (_) {}
  }

  void _upsertIntoLive(IntelEvent ev) {
    final matchesFilter = (_liveCategory == 'all' || ev.category == _liveCategory) && ev.score >= _liveMinScore;
    final idx = _liveEvents.indexWhere((e) => e.id == ev.id);
    if (idx >= 0) {
      _liveEvents[idx] = ev;
    } else if (matchesFilter) {
      _liveEvents.insert(0, ev);
      // Keep strict published_at DESC order
      _liveEvents.sort((a, b) => b.publishedAt != a.publishedAt ? b.publishedAt.compareTo(a.publishedAt) : b.id.compareTo(a.id));
    }
    if (_selectedEvent?.id == ev.id) {
      _selectedEvent = ev;
      _bumpDetail();
    }
  }

  void _handleStreamEvent(String type, IntelEvent ev) {
    _persistLastEventId(ev.id);
    setState(() => _upsertIntoLive(ev));
    if (type == 'event.created' && _shouldFireLocalAlert(ev)) {
      _notifiedIds.add(ev.id);
      if (_notifiedIds.length > 400) _notifiedIds.clear();
      NativeNotifier.instance.showBreakingAlert(ev);
    }
  }

  Future<void> _refreshLiveEvents({bool silent = false}) async {
    if (!silent) {
      setState(() {
        _loadingLive = true;
        _loadError = null;
      });
    }
    try {
      final list = await _api.fetchEvents(
        categories: _liveCategory == 'all' ? null : [_liveCategory],
        minScore: _liveMinScore > 1 ? _liveMinScore : null,
        sort: 'time_desc',
        limit: 40,
      );
      if (!mounted) return;
      final maxId = list.fold<int>(_lastEventId, (m, e) => e.id > m ? e.id : m);
      await _persistLastEventId(maxId);
      setState(() {
        _liveEvents = list;
        _hasMoreLive = list.length >= 40;
        _loadError = null;
        if (_selectedEvent == null && list.isNotEmpty) {
          _selectEvent(list.first);
        }
      });
      if (_liveCategory == 'all' && _liveMinScore <= 1) {
        _cacheLiveEvents(list);
      }
    } catch (e) {
      if (mounted) setState(() => _loadError = '连接服务端失败：$e');
    } finally {
      if (mounted) setState(() => _loadingLive = false);
    }
  }

  Future<void> _loadMoreLive() async {
    if (_liveEvents.isEmpty) return;
    setState(() => _loadingMoreLive = true);
    try {
      final oldestId = _liveEvents.map((e) => e.id).reduce((a, b) => a < b ? a : b);
      final more = await _api.fetchEvents(
        categories: _liveCategory == 'all' ? null : [_liveCategory],
        minScore: _liveMinScore > 1 ? _liveMinScore : null,
        beforeId: oldestId,
        sort: 'time_desc',
        limit: 35,
      );
      if (!mounted) return;
      final existing = _liveEvents.map((e) => e.id).toSet();
      final fresh = more.where((e) => !existing.contains(e.id)).toList();
      setState(() {
        _liveEvents.addAll(fresh);
        _hasMoreLive = more.length >= 35;
      });
    } catch (_) {
    } finally {
      if (mounted) setState(() => _loadingMoreLive = false);
    }
  }

  Future<void> _refreshArchiveEvents() async {
    _loadedArchiveOnce = true;
    setState(() {
      _loadingArchive = true;
      _loadError = null;
    });
    try {
      final q = _searchCtrl.text.trim();
      final list = await _api.fetchEvents(
        query: q.isEmpty ? null : q,
        categories: _archiveCategory == 'all' ? null : [_archiveCategory],
        minScore: _archiveMinScore > 1 ? _archiveMinScore : null,
        fromTs: _dateRange != null ? _dateRange!.start.millisecondsSinceEpoch ~/ 1000 : null,
        toTs: _dateRange != null
            ? _dateRange!.end.add(const Duration(hours: 23, minutes: 59, seconds: 59)).millisecondsSinceEpoch ~/ 1000
            : null,
        entity: _activeEntity,
        bookmarkedOnly: _bookmarkedOnly,
        sort: (q.isNotEmpty && _sortMode == 'time_desc') ? 'relevance' : _sortMode,
        limit: 45,
      );
      if (!mounted) return;
      setState(() {
        _archiveEvents = list;
        _hasMoreArchive = q.isEmpty && list.length >= 45;
      });
    } catch (e) {
      if (mounted) setState(() => _loadError = '检索历史库失败：$e');
    } finally {
      if (mounted) setState(() => _loadingArchive = false);
    }
  }

  Future<void> _loadMoreArchive() async {
    if (_archiveEvents.isEmpty || _searchCtrl.text.trim().isNotEmpty) return;
    setState(() => _loadingMoreArchive = true);
    try {
      final oldestId = _archiveEvents.map((e) => e.id).reduce((a, b) => a < b ? a : b);
      final more = await _api.fetchEvents(
        categories: _archiveCategory == 'all' ? null : [_archiveCategory],
        minScore: _archiveMinScore > 1 ? _archiveMinScore : null,
        fromTs: _dateRange != null ? _dateRange!.start.millisecondsSinceEpoch ~/ 1000 : null,
        toTs: _dateRange != null
            ? _dateRange!.end.add(const Duration(hours: 23, minutes: 59, seconds: 59)).millisecondsSinceEpoch ~/ 1000
            : null,
        entity: _activeEntity,
        bookmarkedOnly: _bookmarkedOnly,
        beforeId: oldestId,
        sort: _sortMode,
        limit: 40,
      );
      if (!mounted) return;
      final existing = _archiveEvents.map((e) => e.id).toSet();
      setState(() {
        _archiveEvents.addAll(more.where((e) => !existing.contains(e.id)));
        _hasMoreArchive = more.length >= 40;
      });
    } catch (_) {
    } finally {
      if (mounted) setState(() => _loadingMoreArchive = false);
    }
  }

  void _pushMobileDetailPage(IntelEvent fallbackEv) {
    Navigator.push(
      context,
      MaterialPageRoute(
        builder: (_) => ValueListenableBuilder<int>(
          valueListenable: _detailTick,
          builder: (ctx, __, ___) => Scaffold(
            appBar: AppBar(title: Text(_selectedEvent?.eventCode ?? fallbackEv.eventCode)),
            body: SafeArea(child: _buildDetailPane()),
          ),
        ),
      ),
    );
  }

  Future<void> _openEventByCode(String code) async {
    if (Platform.isWindows) {
      await WindowsTrayController.instance.showMainWindow();
    }
    try {
      final detail = await _api.fetchEventDetail(code);
      if (!mounted) return;
      _streamNotifier.value = '';
      setState(() {
        _selectedEvent = detail.event;
        _selectedThreads = detail.threads;
        _selectedRelated = detail.related;
        if (_navIndex != 0 && _navIndex != 1) _navIndex = 0;
      });
      _bumpDetail();
      final isWide = MediaQuery.of(context).size.width >= 960;
      if (!isWide) {
        _pushMobileDetailPage(detail.event);
      }
    } catch (_) {}
  }

  Future<void> _selectEvent(IntelEvent ev) async {
    _streamNotifier.value = '';
    setState(() {
      _selectedEvent = ev;
      _selectedThreads = [];
      _selectedRelated = [];
    });
    _bumpDetail();
    try {
      final detail = await _api.fetchEventDetail(ev.eventCode);
      if (!mounted || _selectedEvent?.eventCode != ev.eventCode) return;
      setState(() {
        _selectedEvent = detail.event;
        _selectedThreads = detail.threads;
        _selectedRelated = detail.related;
      });
      _bumpDetail();
    } catch (_) {}
  }

  Future<void> _toggleBookmark(IntelEvent ev) async {
    try {
      final updated = await _api.setBookmark(ev.eventCode, bookmarked: !ev.isBookmarked);
      setState(() {
        final li = _liveEvents.indexWhere((e) => e.id == updated.id);
        if (li >= 0) _liveEvents[li] = updated;
        final ai = _archiveEvents.indexWhere((e) => e.id == updated.id);
        if (ai >= 0) _archiveEvents[ai] = updated;
        if (_selectedEvent?.id == updated.id) _selectedEvent = updated;
      });
      _bumpDetail();
    } catch (_) {}
  }

  /// Isolated, 65ms-throttled SSE streaming so typing AI Markdown NEVER drops frames on 120Hz displays!
  Future<void> _askEventAI(String question) async {
    final ev = _selectedEvent;
    if (ev == null || question.trim().isEmpty || _askingNotifier.value) return;
    FocusManager.instance.primaryFocus?.unfocus();
    _askCtrl.clear();

    _askingNotifier.value = true;
    _streamNotifier.value = '';
    _selectedThreads = [
      ..._selectedThreads,
      EventThreadMsg(
        id: 0,
        role: 'user',
        contentMd: question.trim(),
        modelUsed: '',
        createdAt: DateTime.now().millisecondsSinceEpoch ~/ 1000,
      ),
    ];
    _bumpDetail();
    _scrollDetailToBottomIfNear(force: true);

    final buf = StringBuffer();
    DateTime lastFlush = DateTime.fromMillisecondsSinceEpoch(0);

    try {
      await for (final delta in _api.streamAsk(
        endpointPath: '/api/v1/events/${ev.eventCode}/ask',
        payload: {'question': question.trim(), 'web_search': _webSearchInAsk},
      )) {
        if (!mounted) break;
        buf.write(delta);
        final now = DateTime.now();
        if (now.difference(lastFlush).inMilliseconds >= 65) {
          lastFlush = now;
          _streamNotifier.value = buf.toString();
          _scrollDetailToBottomIfNear();
        }
      }
      final finalText = buf.toString();
      if (mounted && finalText.isNotEmpty) {
        _streamNotifier.value = '';
        _selectedThreads = [
          ..._selectedThreads,
          EventThreadMsg(
            id: 0,
            role: 'assistant',
            contentMd: finalText,
            modelUsed: _strongModel,
            createdAt: DateTime.now().millisecondsSinceEpoch ~/ 1000,
          ),
        ];
        _bumpDetail();
        _scrollDetailToBottomIfNear();
      }
    } catch (e) {
      if (mounted) {
        _streamNotifier.value = '⚠️ 请求失败：$e';
      }
    } finally {
      if (mounted) {
        _askingNotifier.value = false;
        _bumpDetail();
      }
    }
  }

  Future<void> _openHistoricalRAGDialog() async {
    final qCtrl = TextEditingController(text: _searchCtrl.text.trim());
    final ragNotifier = ValueNotifier<String>('');
    bool running = false;
    final cs = Theme.of(context).colorScheme;

    await showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDlg) => AlertDialog(
          backgroundColor: cs.surface,
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(22)),
          title: Row(
            children: [
              Icon(Icons.auto_awesome_rounded, color: cs.primary),
              const SizedBox(width: 8),
              const Text('跨周期历史复盘 AI 问答'),
            ],
          ),
          content: SizedBox(
            width: 640,
            height: 420,
            child: Column(
              children: [
                TextField(
                  controller: qCtrl,
                  decoration: const InputDecoration(
                    hintText: '例如：梳理过去三个月英伟达与 AI 芯片的关键突发脉络',
                  ),
                ),
                const SizedBox(height: 12),
                Expanded(
                  child: Container(
                    width: double.infinity,
                    padding: const EdgeInsets.all(14),
                    decoration: BoxDecoration(
                      color: cs.surfaceContainerLowest,
                      borderRadius: BorderRadius.circular(14),
                      border: Border.all(color: cs.outline),
                    ),
                    child: SingleChildScrollView(
                      child: ValueListenableBuilder<String>(
                        valueListenable: ragNotifier,
                        builder: (_, answer, __) => answer.isEmpty
                            ? Text(
                                '点击下方“开始历史复盘”，将自动检索永久 SQLite 历史库并引用 [#EVT-...] 生成报告（使用模型：$_strongModel）。',
                                style: Theme.of(ctx).textTheme.bodySmall,
                              )
                            : IntelMarkdownView(data: answer),
                      ),
                    ),
                  ),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('关闭')),
            FilledButton.icon(
              onPressed: running
                  ? null
                  : () async {
                      if (qCtrl.text.trim().isEmpty) return;
                      setDlg(() => running = true);
                      ragNotifier.value = '';
                      final buf = StringBuffer();
                      var lastFlush = DateTime.fromMillisecondsSinceEpoch(0);
                      try {
                        await for (final delta in _api.streamAsk(
                          endpointPath: '/api/v1/history/ask',
                          payload: {
                            'question': qCtrl.text.trim(),
                            'entity': _activeEntity ?? '',
                            'from_ts': _dateRange != null ? _dateRange!.start.millisecondsSinceEpoch ~/ 1000 : 0,
                            'to_ts': _dateRange != null ? _dateRange!.end.millisecondsSinceEpoch ~/ 1000 : 0,
                          },
                        )) {
                          buf.write(delta);
                          final now = DateTime.now();
                          if (now.difference(lastFlush).inMilliseconds >= 65) {
                            lastFlush = now;
                            ragNotifier.value = buf.toString();
                          }
                        }
                        ragNotifier.value = buf.toString();
                      } catch (e) {
                        ragNotifier.value = '⚠️ 检索失败：$e';
                      } finally {
                        setDlg(() => running = false);
                      }
                    },
              icon: const Icon(Icons.play_arrow_rounded),
              label: Text(running ? '复盘生成中…' : '开始历史复盘'),
            ),
          ],
        ),
      ),
    );
    ragNotifier.dispose();
  }

  Future<void> _loadDigests() async {
    _loadedDigestsOnce = true;
    try {
      final list = await _api.fetchDigests();
      if (mounted) {
        setState(() {
          _digests = list;
          if (_selectedDigest == null && list.isNotEmpty) _selectedDigest = list.first;
        });
      }
    } catch (_) {}
  }

  Future<void> _generateTodayDigest() async {
    if (_generatingDigest) return;
    setState(() => _generatingDigest = true);
    try {
      final d = await _api.generateTodayDigest();
      await _loadDigests();
      if (!mounted) return;
      setState(() => _selectedDigest = d);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已生成 ${d.digestDate}《${d.title}》（共提炼 ${d.eventCount} 条情报）')),
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('生成今日内参失败：$e')));
      }
    } finally {
      if (mounted) setState(() => _generatingDigest = false);
    }
  }

  Future<void> _loadOverview() async {
    try {
      final ov = await _api.fetchOverview();
      if (mounted) setState(() => _overview = ov);
    } catch (_) {}
  }

  Future<void> _loadModelSettings() async {
    try {
      final res = await _api.fetchModelSettings();
      if (!mounted) return;
      setState(() {
        _modelSettings = res;
        _selectedProvider = res.active.provider;
        _fastModel = res.active.fastModel;
        _fallbackModel = res.active.fallbackModel;
        _strongModel = res.active.strongModel;
      });
    } catch (_) {}
  }

  Future<void> _saveModelSettings() async {
    setState(() => _savingModels = true);
    try {
      final updated = await _api.updateModelSettings(ModelSelection(
        provider: _selectedProvider,
        fastModel: _fastModel,
        fallbackModel: _fallbackModel,
        strongModel: _strongModel,
      ));
      if (!mounted) return;
      setState(() {
        _modelSettings = updated;
        _selectedProvider = updated.active.provider;
        _fastModel = updated.active.fastModel;
        _fallbackModel = updated.active.fallbackModel;
        _strongModel = updated.active.strongModel;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已热同步至后端：初筛 $_fastModel · 兜底 $_fallbackModel · 深度 $_strongModel')),
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('保存模型配置失败：$e')));
      }
    } finally {
      if (mounted) setState(() => _savingModels = false);
    }
  }

  Future<void> _loadNotificationPolicy() async {
    try {
      final p = await _api.fetchNotificationPolicy();
      if (!mounted) return;
      setState(() {
        _serverMinScore = p.minPushScore;
        _serverNotifyCategories = p.categories.toSet();
        _serverEnableFeishu = p.enableFeishu;
        _serverEnableVerify = p.enableVerificationPush;
        _serverEnableDailyDigest = p.enableDailyDigest;
      });
    } catch (_) {}
  }

  Future<void> _saveNotificationPolicy() async {
    setState(() => _savingNotifyPolicy = true);
    try {
      final cats = _serverNotifyCategories.isEmpty
          ? ['finance', 'ai', 'bigtech', 'world']
          : _serverNotifyCategories.toList();
      final updated = await _api.updateNotificationPolicy(NotificationPolicy(
        minPushScore: _serverMinScore,
        categories: cats,
        enableFeishu: _serverEnableFeishu,
        enableVerificationPush: _serverEnableVerify,
        enableDailyDigest: _serverEnableDailyDigest,
      ));
      if (!mounted) return;
      setState(() {
        _serverMinScore = updated.minPushScore;
        _serverNotifyCategories = updated.categories.toSet();
        _serverEnableFeishu = updated.enableFeishu;
        _serverEnableVerify = updated.enableVerificationPush;
        _serverEnableDailyDigest = updated.enableDailyDigest;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('全局通知策略已热同步至服务端（≥${updated.minPushScore}分推送）')),
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('保存全局通知策略失败：$e')));
      }
    } finally {
      if (mounted) setState(() => _savingNotifyPolicy = false);
    }
  }

  Future<void> _saveSettings() async {
    _quietHoursRange = _quietCtrl.text.trim().isEmpty ? '00:30-07:30' : _quietCtrl.text.trim();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('server_url', _urlCtrl.text.trim());
    await prefs.setString('api_token', _tokenCtrl.text.trim());
    await prefs.setInt('min_push_score', _minPushScore);
    await prefs.setStringList('local_notify_categories', _localNotifyCategories.toList());
    await prefs.setBool('android_keep_alive', _androidKeepAlive);
    await prefs.setBool('win_minimize_to_tray', _winMinimizeToTray);
    await prefs.setBool('quiet_hours_enabled', _quietHoursEnabled);
    await prefs.setString('quiet_hours_range', _quietHoursRange);
    await prefs.setBool('critical_bypass_quiet', _criticalBypassQuiet);
    await prefs.setString('github_repo', _repoCtrl.text.trim());
    await prefs.setString('github_token', _ghTokenCtrl.text.trim());

    _api.baseUrl = _urlCtrl.text.trim();
    _api.apiToken = _tokenCtrl.text.trim();
    _connectWebSocket();
    _startFallbackPoller();
    if (Platform.isAndroid) {
      if (_androidKeepAlive && _api.baseUrl.isNotEmpty) {
        await NativeNotifier.instance.startAndroidKeepAlive('实时在线 · 突发秒级监测中');
      } else {
        await NativeNotifier.instance.stopAndroidKeepAlive();
      }
    }
    await Future.wait([_refreshLiveEvents(), _loadOverview(), _loadModelSettings(), _loadNotificationPolicy()]);
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('配置已永久保存并重新建立实时流')),
      );
    }
  }

  void _fireTestNotification() {
    NativeNotifier.instance.showBreakingAlert(IntelEvent(
      id: 999999,
      eventCode: _selectedEvent?.eventCode ?? 'EVT-TEST-LOCAL',
      sourceName: 'local',
      category: 'ai',
      rawTitle: 'Local Native Notification Test',
      zhTitle: '原生系统通知自检成功',
      summary: 'Windows 托盘 WinRT Toast / ColorOS 17 高优横幅通道已就绪',
      impactAnalysis: '收到 P1 突发事件时将通过此通道秒级提醒，点击通知可直达详情页。',
      canonicalUrl: '',
      entities: const [],
      quoteSnapshot: const {},
      score: 9,
      tier: 'P1_CRITICAL',
      isBookmarked: false,
      userNote: '',
      pushStatus: 'pushed',
      verificationStatus: 'none',
      verificationNote: '',
      publishedAt: DateTime.now().millisecondsSinceEpoch ~/ 1000,
      createdAt: DateTime.now().millisecondsSinceEpoch ~/ 1000,
    ));
  }

  Future<void> _checkForUpdates() async {
    final repo = _repoCtrl.text.trim().isNotEmpty ? _repoCtrl.text.trim() : kDefaultGithubRepo;
    final ghToken = _ghTokenCtrl.text.trim();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('github_repo', repo);
    await prefs.setString('github_token', ghToken);

    setState(() => _checkingUpdate = true);
    try {
      final info = await AppUpdater.checkLatest(repoSlug: repo, githubToken: ghToken);
      if (!mounted) return;
      await _showUpdateDialog(info, ghToken);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('检查更新失败：$e')));
      }
    } finally {
      if (mounted) setState(() => _checkingUpdate = false);
    }
  }

  Future<void> _showUpdateDialog(ReleaseInfo info, String ghToken) async {
    double? downloadProgress;
    String? downloadedPath;
    String? dlError;

    await showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDlg) => AlertDialog(
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(22)),
          title: Row(
            children: [
              Icon(
                info.hasUpdate ? Icons.system_update_rounded : Icons.verified_rounded,
                color: info.hasUpdate ? Theme.of(ctx).colorScheme.primary : const Color(0xFF10B981),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  info.hasUpdate ? '发现新版本 ${info.tagName}' : '当前已是最新版 (v$kAppVersion)',
                  style: const TextStyle(fontSize: 17),
                ),
              ),
            ],
          ),
          content: SizedBox(
            width: 500,
            child: SingleChildScrollView(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text('当前版本：v$kAppVersion  →  最新版本：${info.tagName}',
                      style: Theme.of(ctx).textTheme.bodySmall),
                  if (info.assetName != null) ...[
                    const SizedBox(height: 4),
                    Text(
                      '匹配安装包：${info.assetName} (${(info.assetSizeBytes / 1024 / 1024).toStringAsFixed(1)} MB)',
                      style: Theme.of(ctx).textTheme.labelSmall,
                    ),
                  ],
                  const SizedBox(height: 12),
                  Container(
                    width: double.infinity,
                    constraints: const BoxConstraints(maxHeight: 240),
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: Theme.of(ctx).colorScheme.surfaceContainerLowest,
                      borderRadius: BorderRadius.circular(12),
                      border: Border.all(color: Theme.of(ctx).colorScheme.outline),
                    ),
                    child: SingleChildScrollView(child: IntelMarkdownView(data: info.bodyMd)),
                  ),
                  if (downloadProgress != null) ...[
                    const SizedBox(height: 14),
                    LinearProgressIndicator(value: downloadProgress, borderRadius: BorderRadius.circular(999)),
                    const SizedBox(height: 6),
                    Text(
                      downloadedPath != null
                          ? '已下载至：$downloadedPath'
                          : '正在下载安装包… ${(downloadProgress! * 100).toStringAsFixed(0)}%',
                      style: Theme.of(ctx).textTheme.labelSmall,
                    ),
                  ],
                  if (dlError != null) ...[
                    const SizedBox(height: 8),
                    Text(dlError!, style: TextStyle(color: Theme.of(ctx).colorScheme.error, fontSize: 12)),
                  ],
                ],
              ),
            ),
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('关闭')),
            OutlinedButton(
              onPressed: () => launchUrl(Uri.parse(info.htmlUrl), mode: LaunchMode.externalApplication),
              child: const Text('Release 页面'),
            ),
            if (Platform.isWindows && downloadedPath != null)
              FilledButton.icon(
                onPressed: () async {
                  await Process.start(downloadedPath!, [], mode: ProcessStartMode.detached);
                  if (ctx.mounted) Navigator.pop(ctx);
                },
                icon: const Icon(Icons.install_desktop_rounded, size: 18),
                label: const Text('立即运行安装'),
              )
            else if (info.assetDownloadUrl != null)
              FilledButton.icon(
                onPressed: downloadProgress != null
                    ? null
                    : () async {
                        if (Platform.isWindows) {
                          setDlg(() {
                            downloadProgress = 0.01;
                            dlError = null;
                          });
                          try {
                            final saved = await AppUpdater.downloadWindowsInstaller(
                              release: info,
                              githubToken: ghToken,
                              onProgress: (p) => setDlg(() => downloadProgress = p),
                            );
                            setDlg(() => downloadedPath = saved);
                          } catch (e) {
                            setDlg(() {
                              downloadProgress = null;
                              dlError = '下载失败：$e';
                            });
                          }
                        } else {
                          await launchUrl(Uri.parse(info.assetDownloadUrl!), mode: LaunchMode.externalApplication);
                        }
                      },
                icon: const Icon(Icons.download_rounded, size: 18),
                label: Text(Platform.isWindows ? '下载安装包' : '下载最新 APK'),
              ),
          ],
        ),
      ),
    );
  }

  void _cycleThemeMode() {
    final next = switch (widget.themeMode) {
      ThemeMode.system => ThemeMode.light,
      ThemeMode.light => ThemeMode.dark,
      ThemeMode.dark => ThemeMode.system,
    };
    widget.onThemeModeChanged(next);
  }

  IconData _themeIcon() => switch (widget.themeMode) {
        ThemeMode.light => Icons.light_mode_rounded,
        ThemeMode.dark => Icons.dark_mode_rounded,
        ThemeMode.system => Icons.brightness_auto_rounded,
      };

  String _themeLabel() => switch (widget.themeMode) {
        ThemeMode.light => '浅色',
        ThemeMode.dark => '深色',
        ThemeMode.system => '跟随系统',
      };

  void _onNavChanged(int idx) {
    if (_navIndex == idx) return;
    setState(() => _navIndex = idx);
    if (idx == 1 && !_loadedArchiveOnce) _refreshArchiveEvents();
    if (idx == 2 && !_loadedDigestsOnce) _loadDigests();
    if (idx == 3) {
      _loadOverview();
      _loadModelSettings();
      _loadNotificationPolicy();
    }
  }

  @override
  Widget build(BuildContext context) {
    final isWide = MediaQuery.of(context).size.width >= 960;
    final cs = Theme.of(context).colorScheme;

    return Scaffold(
      body: Column(
        children: [
          if (Platform.isWindows) _buildWindowsTitleBar(cs),
          Expanded(child: isWide ? _buildDesktopBody(cs) : _buildMobileBody(cs)),
        ],
      ),
      bottomNavigationBar: isWide
          ? null
          : Container(
              decoration: BoxDecoration(border: Border(top: BorderSide(color: cs.outline, width: 0.7))),
              child: NavigationBar(
                selectedIndex: _navIndex,
                onDestinationSelected: _onNavChanged,
                destinations: const [
                  NavigationDestination(icon: Icon(Icons.bolt_rounded), label: '实时'),
                  NavigationDestination(icon: Icon(Icons.manage_search_rounded), label: '历史库'),
                  NavigationDestination(icon: Icon(Icons.menu_book_rounded), label: '内参'),
                  NavigationDestination(icon: Icon(Icons.tune_rounded), label: '系统'),
                ],
              ),
            ),
    );
  }

  Widget _buildWindowsTitleBar(ColorScheme cs) {
    return Container(
      height: 42,
      decoration: BoxDecoration(
        color: cs.surfaceContainerLow,
        border: Border(bottom: BorderSide(color: cs.outline, width: 1)),
      ),
      child: Row(
        children: [
          Expanded(
            child: DragToMoveArea(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 14),
                child: Row(
                  children: [
                    Image.asset('assets/app_icon_squircle.png', width: 24, height: 24, filterQuality: FilterQuality.high),
                    const SizedBox(width: 10),
                    Text('IntelHub', style: TextStyle(fontWeight: FontWeight.w600, fontSize: 13.5, color: cs.onSurface)),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                      decoration: BoxDecoration(
                        color: (_wsConnected ? const Color(0xFF10B981) : const Color(0xFFF59E0B)).withValues(alpha: 0.14),
                        borderRadius: BorderRadius.circular(999),
                      ),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Container(
                            width: 6,
                            height: 6,
                            decoration: BoxDecoration(
                              color: _wsConnected ? const Color(0xFF10B981) : const Color(0xFFF59E0B),
                              shape: BoxShape.circle,
                            ),
                          ),
                          const SizedBox(width: 5),
                          Text(
                            _wsConnected ? '实时在线' : '重连中',
                            style: TextStyle(
                              fontSize: 11,
                              fontWeight: FontWeight.w500,
                              color: _wsConnected ? const Color(0xFF10B981) : const Color(0xFFD97706),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
          Tooltip(
            message: '切换主题（当前：${_themeLabel()}）',
            child: _TitleBarIconBtn(icon: _themeIcon(), color: cs.onSurface, onTap: _cycleThemeMode),
          ),
          _TitleBarIconBtn(icon: Icons.remove_rounded, color: cs.onSurface, onTap: () => windowManager.minimize()),
          _TitleBarIconBtn(
            icon: _isMaximized ? Icons.filter_none_rounded : Icons.crop_square_rounded,
            iconSize: 15,
            color: cs.onSurface,
            onTap: () async {
              if (await windowManager.isMaximized()) {
                await windowManager.unmaximize();
              } else {
                await windowManager.maximize();
              }
            },
          ),
          Tooltip(
            message: _winMinimizeToTray ? '最小化到系统托盘保持通知' : '关闭窗口',
            child: _TitleBarIconBtn(
              icon: Icons.close_rounded,
              color: cs.onSurface,
              isClose: true,
              onTap: () => windowManager.close(),
            ),
          ),
        ],
      ),
    );
  }

  /// IndexedStack keeps scroll positions & widgets warm across tabs (0ms instant switch!).
  Widget _buildDesktopBody(ColorScheme cs) {
    final detailWidget = RepaintBoundary(
      child: ValueListenableBuilder<int>(
        valueListenable: _detailTick,
        builder: (_, __, ___) => _buildDetailPane(),
      ),
    );

    return Row(
      children: [
        Container(
          width: 78,
          decoration: BoxDecoration(
            color: cs.surfaceContainerLow,
            border: Border(right: BorderSide(color: cs.outline, width: 1)),
          ),
          child: Column(
            children: [
              const SizedBox(height: 14),
              _RailItem(icon: Icons.bolt_rounded, label: '实时流', selected: _navIndex == 0, onTap: () => _onNavChanged(0)),
              _RailItem(icon: Icons.manage_search_rounded, label: '历史库', selected: _navIndex == 1, onTap: () => _onNavChanged(1)),
              _RailItem(icon: Icons.menu_book_rounded, label: '内参', selected: _navIndex == 2, onTap: () => _onNavChanged(2)),
              _RailItem(icon: Icons.tune_rounded, label: '系统', selected: _navIndex == 3, onTap: () => _onNavChanged(3)),
            ],
          ),
        ),
        Expanded(
          child: IndexedStack(
            index: _navIndex,
            children: [
              Row(
                children: [
                  SizedBox(width: 440, child: _buildEventListPane(isArchiveMode: false)),
                  VerticalDivider(width: 1, color: cs.outline),
                  Expanded(child: detailWidget),
                ],
              ),
              Row(
                children: [
                  SizedBox(width: 440, child: _buildEventListPane(isArchiveMode: true)),
                  VerticalDivider(width: 1, color: cs.outline),
                  Expanded(child: detailWidget),
                ],
              ),
              _buildDigestView(cs),
              _buildSettingsAndDashboardView(cs),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildMobileBody(ColorScheme cs) {
    return SafeArea(
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(18, 10, 12, 4),
            child: Row(
              children: [
                Text(
                  switch (_navIndex) {
                    0 => '实时突发',
                    1 => '历史情报库',
                    2 => '每日内参',
                    _ => '系统与模型',
                  },
                  style: Theme.of(context).textTheme.headlineSmall,
                ),
                const SizedBox(width: 8),
                Container(
                  width: 8,
                  height: 8,
                  decoration: BoxDecoration(
                    color: _wsConnected ? const Color(0xFF10B981) : const Color(0xFFF59E0B),
                    shape: BoxShape.circle,
                  ),
                ),
                const Spacer(),
                IconButton(
                  tooltip: '主题：${_themeLabel()}',
                  onPressed: _cycleThemeMode,
                  icon: Icon(_themeIcon()),
                ),
              ],
            ),
          ),
          Expanded(
            child: IndexedStack(
              index: _navIndex,
              children: [
                _buildEventListPane(isArchiveMode: false, openDetailAsPage: true, showHeaderTitle: false),
                _buildEventListPane(isArchiveMode: true, openDetailAsPage: true, showHeaderTitle: false),
                _buildDigestView(cs),
                _buildSettingsAndDashboardView(cs),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildEventListPane({
    required bool isArchiveMode,
    bool openDetailAsPage = false,
    bool showHeaderTitle = true,
  }) {
    final cs = Theme.of(context).colorScheme;
    final list = isArchiveMode ? _archiveEvents : _liveEvents;
    final loading = isArchiveMode ? _loadingArchive : _loadingLive;
    final loadingMore = isArchiveMode ? _loadingMoreArchive : _loadingMoreLive;
    final ctrl = isArchiveMode ? _archiveScrollCtrl : _liveScrollCtrl;
    final curCat = isArchiveMode ? _archiveCategory : _liveCategory;
    final curMinScore = isArchiveMode ? _archiveMinScore : _liveMinScore;
    final onRefresh = isArchiveMode ? _refreshArchiveEvents : () => _refreshLiveEvents();

    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (showHeaderTitle)
                Row(
                  children: [
                    Text(isArchiveMode ? '全量历史情报库' : '实时突发流', style: Theme.of(context).textTheme.titleLarge),
                    const Spacer(),
                    if (isArchiveMode)
                      TextButton.icon(
                        onPressed: _openHistoricalRAGDialog,
                        icon: const Icon(Icons.auto_awesome_rounded, size: 16),
                        label: const Text('AI 复盘'),
                      ),
                    IconButton(tooltip: '刷新', onPressed: onRefresh, icon: const Icon(Icons.refresh_rounded, size: 20)),
                  ],
                ),
              if (isArchiveMode) ...[
                const SizedBox(height: 8),
                TextField(
                  controller: _searchCtrl,
                  onSubmitted: (_) => _refreshArchiveEvents(),
                  decoration: InputDecoration(
                    hintText: '全文检索：NVDA / 反垄断 / OpenAI...',
                    prefixIcon: const Icon(Icons.search_rounded, size: 20),
                    suffixIcon: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        if (!showHeaderTitle)
                          IconButton(
                            tooltip: 'AI 历史复盘',
                            icon: const Icon(Icons.auto_awesome_rounded, size: 18),
                            onPressed: _openHistoricalRAGDialog,
                          ),
                        IconButton(icon: const Icon(Icons.arrow_forward_rounded, size: 18), onPressed: _refreshArchiveEvents),
                      ],
                    ),
                  ),
                ),
                const SizedBox(height: 10),
                Wrap(
                  spacing: 6,
                  runSpacing: 6,
                  children: [
                    _CompactPill(
                      label: _dateRange == null
                          ? '全部日期'
                          : '${_fmtMonthDay.format(_dateRange!.start)}–${_fmtMonthDay.format(_dateRange!.end)}',
                      icon: Icons.date_range_rounded,
                      selected: _dateRange != null,
                      onTap: () async {
                        final picked = await showDateRangePicker(
                          context: context,
                          firstDate: DateTime(2024, 1, 1),
                          lastDate: DateTime.now().add(const Duration(days: 1)),
                          initialDateRange: _dateRange,
                        );
                        if (picked != null) {
                          setState(() => _dateRange = picked);
                          _refreshArchiveEvents();
                        }
                      },
                    ),
                    if (_dateRange != null)
                      _CompactPill(
                        label: '重置日期',
                        selected: false,
                        onTap: () {
                          setState(() => _dateRange = null);
                          _refreshArchiveEvents();
                        },
                      ),
                    _CompactPill(
                      label: '收藏',
                      icon: Icons.bookmark_rounded,
                      selected: _bookmarkedOnly,
                      onTap: () {
                        setState(() => _bookmarkedOnly = !_bookmarkedOnly);
                        _refreshArchiveEvents();
                      },
                    ),
                    _CompactPill(
                      label: switch (_sortMode) {
                        'time_asc' => '正序复盘',
                        'score_desc' => '评分最高',
                        _ => '最新优先',
                      },
                      icon: Icons.sort_rounded,
                      selected: _sortMode != 'time_desc',
                      onTap: () {
                        setState(() {
                          _sortMode = switch (_sortMode) {
                            'time_desc' => 'score_desc',
                            'score_desc' => 'time_asc',
                            _ => 'time_desc',
                          };
                        });
                        _refreshArchiveEvents();
                      },
                    ),
                    if (_activeEntity != null)
                      _CompactPill(
                        label: '#$_activeEntity ×',
                        selected: true,
                        onTap: () {
                          setState(() => _activeEntity = null);
                          _refreshArchiveEvents();
                        },
                      ),
                  ],
                ),
              ],
              const SizedBox(height: 10),
              SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                child: Row(
                  children: [
                    for (final pair in const [
                      ('all', '全部'),
                      ('finance', '金融'),
                      ('ai', 'AI'),
                      ('bigtech', '大厂'),
                      ('world', '全球'),
                    ])
                      Padding(
                        padding: const EdgeInsets.only(right: 6),
                        child: _CompactPill(
                          label: pair.$2,
                          selected: curCat == pair.$1,
                          onTap: () {
                            setState(() {
                              if (isArchiveMode) {
                                _archiveCategory = pair.$1;
                              } else {
                                _liveCategory = pair.$1;
                              }
                            });
                            onRefresh();
                          },
                        ),
                      ),
                    _CompactPill(
                      label: '≥8分突发',
                      selected: curMinScore >= 8,
                      onTap: () {
                        setState(() {
                          if (isArchiveMode) {
                            _archiveMinScore = _archiveMinScore >= 8 ? 1 : 8;
                          } else {
                            _liveMinScore = _liveMinScore >= 8 ? 1 : 8;
                          }
                        });
                        onRefresh();
                      },
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
        if (_loadError != null)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
            child: Text(_loadError!, style: TextStyle(color: cs.error, fontSize: 12)),
          ),
        Expanded(
          child: loading && list.isEmpty
              ? const Center(child: CircularProgressIndicator())
              : list.isEmpty
                  ? Center(child: Text('暂无匹配情报，请在「系统」页检查连接配置', style: Theme.of(context).textTheme.bodySmall))
                  : RefreshIndicator(
                      onRefresh: onRefresh,
                      child: ListView.builder(
                        controller: ctrl,
                        cacheExtent: 800,
                        physics: const AlwaysScrollableScrollPhysics(parent: BouncingScrollPhysics()),
                        padding: const EdgeInsets.fromLTRB(14, 4, 14, 24),
                        itemCount: list.length + (loadingMore ? 1 : 0),
                        itemBuilder: (ctx, i) {
                          if (i >= list.length) {
                            return const Padding(
                              padding: EdgeInsets.symmetric(vertical: 16),
                              child: Center(child: SizedBox(width: 20, height: 20, child: CircularProgressIndicator(strokeWidth: 2))),
                            );
                          }
                          final ev = list[i];
                          return RepaintBoundary(
                            key: ValueKey(ev.id),
                            child: _EventCard(
                              event: ev,
                              selected: _selectedEvent?.id == ev.id,
                              onTap: () {
                                _selectEvent(ev);
                                if (openDetailAsPage) {
                                  _pushMobileDetailPage(ev);
                                }
                              },
                              onBookmark: () => _toggleBookmark(ev),
                            ),
                          );
                        },
                      ),
                    ),
        ),
      ],
    );
  }

  Widget _buildDetailPane() {
    final cs = Theme.of(context).colorScheme;
    final ev = _selectedEvent;
    if (ev == null) {
      return Center(
        child: Text('选择左侧任意突发事件查看详情、实体历史时间轴与 AI 深度研判', style: Theme.of(context).textTheme.bodySmall),
      );
    }
    final dtStr = _fmtFull.format(DateTime.fromMillisecondsSinceEpoch(ev.publishedAt * 1000));

    return Column(
      children: [
        Expanded(
          child: ListView(
            controller: _detailScrollCtrl,
            physics: const BouncingScrollPhysics(),
            padding: const EdgeInsets.all(22),
            children: [
              Row(
                children: [
                  _ScoreBadge(score: ev.score),
                  const SizedBox(width: 8),
                  Container(
                    padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                    decoration: BoxDecoration(
                      color: cs.surfaceContainer,
                      borderRadius: BorderRadius.circular(999),
                    ),
                    child: Text(ev.categoryLabel, style: Theme.of(context).textTheme.labelMedium),
                  ),
                  const SizedBox(width: 10),
                  Text(ev.eventCode, style: Theme.of(context).textTheme.labelSmall),
                  const Spacer(),
                  IconButton(
                    tooltip: '收藏 / 星标',
                    onPressed: () => _toggleBookmark(ev),
                    icon: Icon(
                      ev.isBookmarked ? Icons.bookmark_rounded : Icons.bookmark_border_rounded,
                      color: ev.isBookmarked ? const Color(0xFFF59E0B) : cs.onSurface.withValues(alpha: 0.65),
                    ),
                  ),
                  if (ev.canonicalUrl.isNotEmpty)
                    FilledButton.tonalIcon(
                      onPressed: () => launchUrl(Uri.parse(ev.canonicalUrl), mode: LaunchMode.externalApplication),
                      icon: const Icon(Icons.open_in_new_rounded, size: 16),
                      label: const Text('原文'),
                    ),
                ],
              ),
              const SizedBox(height: 14),
              SelectableText(ev.zhTitle, style: Theme.of(context).textTheme.headlineSmall),
              if (ev.rawTitle.isNotEmpty && ev.rawTitle != ev.zhTitle) ...[
                const SizedBox(height: 6),
                SelectableText(ev.rawTitle, style: Theme.of(context).textTheme.bodySmall),
              ],
              const SizedBox(height: 8),
              Text('信源：${ev.sourceName}  ·  发布时间：$dtStr', style: Theme.of(context).textTheme.labelSmall),
              const SizedBox(height: 18),
              if (ev.impactAnalysis.isNotEmpty)
                Container(
                  padding: const EdgeInsets.all(16),
                  decoration: BoxDecoration(
                    color: cs.primary.withValues(alpha: Theme.of(context).brightness == Brightness.dark ? 0.12 : 0.06),
                    borderRadius: BorderRadius.circular(16),
                    border: Border.all(color: cs.primary.withValues(alpha: 0.25)),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('核心影响推演', style: TextStyle(fontWeight: FontWeight.w600, fontSize: 13, color: cs.primary)),
                      const SizedBox(height: 6),
                      SelectableText(ev.impactAnalysis, style: Theme.of(context).textTheme.bodyLarge),
                    ],
                  ),
                ),
              if (ev.verificationNote.isNotEmpty) ...[
                const SizedBox(height: 12),
                Container(
                  padding: const EdgeInsets.all(14),
                  decoration: BoxDecoration(
                    color: (ev.verificationStatus == 'confirmed' ? const Color(0xFF10B981) : const Color(0xFFF59E0B))
                        .withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(16),
                    border: Border.all(
                      color: (ev.verificationStatus == 'confirmed' ? const Color(0xFF10B981) : const Color(0xFFF59E0B))
                          .withValues(alpha: 0.4),
                    ),
                  ),
                  child: Text(
                    '${ev.verificationStatus == 'confirmed' ? '权威多源交叉核验：' : '核验补充：'}${ev.verificationNote}',
                    style: Theme.of(context).textTheme.bodyMedium,
                  ),
                ),
              ],
              const SizedBox(height: 16),
              SelectableText(ev.summary, style: Theme.of(context).textTheme.bodyLarge),
              if (ev.entities.isNotEmpty) ...[
                const SizedBox(height: 18),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: ev.entities
                      .map((ent) => _CompactPill(
                            label: '#$ent 历史时间轴',
                            icon: Icons.timeline_rounded,
                            selected: false,
                            onTap: () {
                              setState(() {
                                _navIndex = 1;
                                _activeEntity = ent;
                              });
                              _refreshArchiveEvents();
                            },
                          ))
                      .toList(),
                ),
              ],
              if (_selectedRelated.isNotEmpty) ...[
                const SizedBox(height: 22),
                Text('同实体历史关联脉络', style: Theme.of(context).textTheme.titleSmall),
                const SizedBox(height: 8),
                for (final rel in _selectedRelated.take(4))
                  ListTile(
                    dense: true,
                    contentPadding: EdgeInsets.zero,
                    leading: _ScoreBadge(score: rel.score),
                    title: Text(rel.zhTitle, maxLines: 1, overflow: TextOverflow.ellipsis),
                    subtitle: Text(_fmtFull.format(DateTime.fromMillisecondsSinceEpoch(rel.publishedAt * 1000))),
                    onTap: () => _selectEvent(rel),
                  ),
              ],
              Divider(height: 36, color: cs.outline),
              Row(
                children: [
                  Text('AI 情报官深度追问', style: Theme.of(context).textTheme.titleMedium),
                  const SizedBox(width: 8),
                  Text('当前模型：$_strongModel', style: Theme.of(context).textTheme.labelSmall),
                ],
              ),
              const SizedBox(height: 10),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final preset in const [
                    '分析对相关美股标的与产业链的影响',
                    '核查背景脉络与历史同类事件演变',
                    '总结核心风险点与后续关注信号',
                  ])
                    _CompactPill(label: preset, selected: false, onTap: () => _askEventAI(preset)),
                ],
              ),
              const SizedBox(height: 14),
              for (final msg in _selectedThreads) RepaintBoundary(child: _ThreadBubble(msg: msg)),
              // Isolated streaming region: updates at 65ms throttle without touching any other widget!
              ValueListenableBuilder<bool>(
                valueListenable: _askingNotifier,
                builder: (_, asking, __) => ValueListenableBuilder<String>(
                  valueListenable: _streamNotifier,
                  builder: (_, streaming, __) {
                    if (asking && streaming.isEmpty) {
                      return Container(
                        margin: const EdgeInsets.only(bottom: 10),
                        padding: const EdgeInsets.all(14),
                        decoration: BoxDecoration(
                          color: cs.surface,
                          borderRadius: BorderRadius.circular(16),
                          border: Border.all(color: cs.primary.withValues(alpha: 0.35)),
                        ),
                        child: Row(
                          children: [
                            SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2, color: cs.primary)),
                            const SizedBox(width: 12),
                            Expanded(
                              child: Text(
                                _webSearchInAsk
                                    ? '正在联网检索最新信源并调用 $_strongModel 深度研判…'
                                    : '正在调用 $_strongModel 深度思考中…',
                                style: Theme.of(context).textTheme.bodySmall?.copyWith(color: cs.primary),
                              ),
                            ),
                          ],
                        ),
                      );
                    }
                    if (streaming.isEmpty) return const SizedBox.shrink();
                    return Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        RepaintBoundary(
                          child: _ThreadBubble(
                            msg: EventThreadMsg(id: 0, role: 'assistant', contentMd: streaming, modelUsed: _strongModel, createdAt: 0),
                          ),
                        ),
                        if (asking)
                          Padding(
                            padding: const EdgeInsets.only(left: 4, bottom: 12),
                            child: Row(
                              children: [
                                SizedBox(width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 1.8, color: cs.primary)),
                                const SizedBox(width: 8),
                                Text('$_strongModel 正在流式生成…', style: Theme.of(context).textTheme.labelSmall),
                              ],
                            ),
                          ),
                      ],
                    );
                  },
                ),
              ),
            ],
          ),
        ),
        Container(
          padding: const EdgeInsets.fromLTRB(16, 10, 16, 14),
          decoration: BoxDecoration(
            color: cs.surfaceContainerLow,
            border: Border(top: BorderSide(color: cs.outline)),
          ),
          child: Row(
            children: [
              _CompactPill(
                label: '联网',
                icon: Icons.public_rounded,
                selected: _webSearchInAsk,
                onTap: () {
                  setState(() => _webSearchInAsk = !_webSearchInAsk);
                  _bumpDetail();
                },
              ),
              const SizedBox(width: 10),
              Expanded(
                child: TextField(
                  controller: _askCtrl,
                  onSubmitted: _askEventAI,
                  decoration: const InputDecoration(hintText: '针对本事件继续追问…', isDense: true),
                ),
              ),
              const SizedBox(width: 10),
              ValueListenableBuilder<bool>(
                valueListenable: _askingNotifier,
                builder: (_, asking, __) => FilledButton.icon(
                  onPressed: asking ? null : () => _askEventAI(_askCtrl.text),
                  icon: asking
                      ? const SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                      : const Icon(Icons.send_rounded, size: 16),
                  label: Text(asking ? '分析中' : '追问'),
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildDigestView(ColorScheme cs) {
    final isWide = MediaQuery.of(context).size.width >= 900;
    final listWidget = Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(16),
          child: Row(
            children: [
              Text('每日内参归档', style: Theme.of(context).textTheme.titleMedium),
              const Spacer(),
              FilledButton.tonalIcon(
                onPressed: _generatingDigest ? null : _generateTodayDigest,
                icon: _generatingDigest
                    ? const SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.auto_awesome_rounded, size: 16),
                label: Text(_generatingDigest ? '生成中…' : '生成今日内参'),
              ),
              const SizedBox(width: 4),
              IconButton(onPressed: _loadDigests, icon: const Icon(Icons.refresh_rounded, size: 18)),
            ],
          ),
        ),
        Expanded(
          child: _digests.isEmpty
              ? Center(child: Text('每日 21:00 自动汇总，也可点右上角“生成今日内参”立即生成', style: Theme.of(context).textTheme.bodySmall))
              : ListView.builder(
                  physics: const BouncingScrollPhysics(),
                  padding: const EdgeInsets.symmetric(horizontal: 10),
                  itemCount: _digests.length,
                  itemBuilder: (_, i) {
                    final d = _digests[i];
                    final sel = _selectedDigest?.id == d.id;
                    return Container(
                      margin: const EdgeInsets.only(bottom: 6),
                      decoration: BoxDecoration(
                        color: sel ? cs.primary.withValues(alpha: 0.12) : Colors.transparent,
                        borderRadius: BorderRadius.circular(14),
                      ),
                      child: ListTile(
                        title: Text(d.digestDate, style: const TextStyle(fontWeight: FontWeight.w600)),
                        subtitle: Text('${d.title} (${d.eventCount}条)', maxLines: 1, overflow: TextOverflow.ellipsis),
                        onTap: () {
                          setState(() => _selectedDigest = d);
                          if (!isWide) {
                            Navigator.push(
                              context,
                              MaterialPageRoute(
                                builder: (_) => Scaffold(
                                  appBar: AppBar(title: Text('${d.digestDate} 内参')),
                                  body: SafeArea(
                                    child: ListView(
                                      padding: const EdgeInsets.all(20),
                                      children: [
                                        Text(d.title, style: Theme.of(context).textTheme.headlineSmall),
                                        const SizedBox(height: 16),
                                        IntelMarkdownView(data: d.contentMd),
                                      ],
                                    ),
                                  ),
                                ),
                              ),
                            );
                          }
                        },
                      ),
                    );
                  },
                ),
        ),
      ],
    );

    final contentWidget = _selectedDigest == null
        ? Center(child: Text('暂无选中的每日内参', style: Theme.of(context).textTheme.bodySmall))
        : ListView(
            padding: const EdgeInsets.all(24),
            children: [
              Text('${_selectedDigest!.digestDate} · ${_selectedDigest!.title}',
                  style: Theme.of(context).textTheme.headlineSmall),
              const SizedBox(height: 16),
              IntelMarkdownView(data: _selectedDigest!.contentMd),
            ],
          );

    if (!isWide) return listWidget;
    return Row(
      children: [
        SizedBox(width: 300, child: listWidget),
        VerticalDivider(width: 1, color: cs.outline),
        Expanded(child: contentWidget),
      ],
    );
  }

  Widget _buildSettingsAndDashboardView(ColorScheme cs) {
    final ov = _overview;
    final budgetPct = ov == null || ov.budget.monthlyCapUsd <= 0
        ? 0.0
        : (ov.budget.totalUsd / ov.budget.monthlyCapUsd).clamp(0.0, 1.0);

    final providers = _modelSettings?.providers ?? const <ModelProviderCatalog>[];
    final currentCatalog = providers.where((p) => p.name == _selectedProvider).firstOrNull;
    final availableModels = <String>{
      ...?currentCatalog?.models,
      if (_fastModel.isNotEmpty) _fastModel,
      if (_fallbackModel.isNotEmpty) _fallbackModel,
      if (_strongModel.isNotEmpty) _strongModel,
    }.toList()
      ..sort();

    return ListView(
      physics: const BouncingScrollPhysics(),
      padding: const EdgeInsets.all(22),
      children: [
        // 1. Appearance
        Text('外观与显示', style: Theme.of(context).textTheme.titleLarge),
        const SizedBox(height: 10),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(18),
            child: Row(
              children: [
                const Icon(Icons.palette_outlined),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('主题模式', style: Theme.of(context).textTheme.titleSmall),
                      Text('支持跟随系统、日光浅色与星夜深色模式', style: Theme.of(context).textTheme.bodySmall),
                    ],
                  ),
                ),
                SegmentedButton<ThemeMode>(
                  segments: const [
                    ButtonSegment(value: ThemeMode.system, label: Text('系统')),
                    ButtonSegment(value: ThemeMode.light, label: Text('浅色')),
                    ButtonSegment(value: ThemeMode.dark, label: Text('深色')),
                  ],
                  selected: {widget.themeMode},
                  onSelectionChanged: (s) => widget.onThemeModeChanged(s.first),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 22),

        // 2. Global Server Notification Level & Category Policy
        Row(
          children: [
            Text('全局消息通知级别与领域（同步服务端）', style: Theme.of(context).textTheme.titleLarge),
            const Spacer(),
            IconButton(tooltip: '刷新服务端通知策略', onPressed: _loadNotificationPolicy, icon: const Icon(Icons.refresh_rounded, size: 18)),
          ],
        ),
        const SizedBox(height: 4),
        Text('控制服务端飞书卡片 / FCM 推送以及每日 21:00 自动内参的触发级别与关注领域，保存后全通道即时生效。',
            style: Theme.of(context).textTheme.bodySmall),
        const SizedBox(height: 10),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(18),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('服务端全局推送最低级别（当前：≥ $_serverMinScore 分）', style: Theme.of(context).textTheme.titleSmall),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final preset in const [
                      (9, '🔴 仅特急 (9-10分)'),
                      (8, '🟠 重要突发 (≥8分)'),
                      (7, '🔵 精选重点 (≥7分)'),
                      (6, '⚪ 次级动态 (≥6分)'),
                    ])
                      _CompactPill(
                        label: preset.$2,
                        selected: _serverMinScore == preset.$1,
                        onTap: () => setState(() => _serverMinScore = preset.$1),
                      ),
                  ],
                ),
                const SizedBox(height: 14),
                Text('服务端推送关注领域', style: Theme.of(context).textTheme.titleSmall),
                const SizedBox(height: 8),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final cat in const [
                      ('finance', '金融/美股'),
                      ('ai', 'AI 行业'),
                      ('bigtech', '大厂动态'),
                      ('world', '全球突发'),
                    ])
                      _CompactPill(
                        label: cat.$2,
                        selected: _serverNotifyCategories.contains(cat.$1),
                        onTap: () => setState(() {
                          if (_serverNotifyCategories.contains(cat.$1)) {
                            if (_serverNotifyCategories.length > 1) _serverNotifyCategories.remove(cat.$1);
                          } else {
                            _serverNotifyCategories.add(cat.$1);
                          }
                        }),
                      ),
                  ],
                ),
                const Divider(height: 24),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('飞书卡片实时推送'),
                  subtitle: const Text('达到上述级别与领域时，推送飞书交互卡片（走 OPPO Push 极速触达）'),
                  value: _serverEnableFeishu,
                  onChanged: (v) => setState(() => _serverEnableFeishu = v),
                ),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('9+ 分权威多源交叉核验补充'),
                  subtitle: const Text('自动在原新闻话题内追加或原地更新核验结论'),
                  value: _serverEnableVerify,
                  onChanged: (v) => setState(() => _serverEnableVerify = v),
                ),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('每日 21:00 自动生成《今日情报内参》'),
                  subtitle: const Text('自动汇总 5-7 分次级情报与当日 P1 精选并写入内参归档'),
                  value: _serverEnableDailyDigest,
                  onChanged: (v) => setState(() => _serverEnableDailyDigest = v),
                ),
                const SizedBox(height: 10),
                FilledButton.icon(
                  onPressed: _savingNotifyPolicy ? null : _saveNotificationPolicy,
                  icon: const Icon(Icons.cloud_done_rounded, size: 18),
                  label: Text(_savingNotifyPolicy ? '正在同步到服务端…' : '保存全局通知策略'),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 22),

        // 3. Isolated 3-Slot LLM Model Service
        Row(
          children: [
            Text('AI 模型服务配置（实时同步后端）', style: Theme.of(context).textTheme.titleLarge),
            const Spacer(),
            IconButton(tooltip: '重新拉取订阅模型列表', onPressed: _loadModelSettings, icon: const Icon(Icons.refresh_rounded, size: 18)),
          ],
        ),
        const SizedBox(height: 4),
        Text('此处配置仅作用于情报系统的初筛打分与深度研判，完全隔离，不会影响你在其他地方与 Hermes 对话使用的模型。',
            style: Theme.of(context).textTheme.bodySmall),
        const SizedBox(height: 10),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(18),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (providers.isNotEmpty) ...[
                  _ModelDropdown(
                    label: '模型订阅渠道 (Provider)',
                    value: _selectedProvider,
                    items: providers.map((p) => p.name).toList(),
                    onChanged: (v) {
                      if (v != null) setState(() => _selectedProvider = v);
                    },
                  ),
                  const SizedBox(height: 14),
                ],
                _ModelDropdown(
                  label: '初筛裁判模型 (Fast Model · 推荐 Flash / Free 档)',
                  value: _fastModel,
                  items: availableModels,
                  onChanged: (v) {
                    if (v != null) setState(() => _fastModel = v);
                  },
                ),
                const SizedBox(height: 14),
                _ModelDropdown(
                  label: '兜底备选模型 (Fallback Model)',
                  value: _fallbackModel,
                  items: availableModels,
                  onChanged: (v) {
                    if (v != null) setState(() => _fallbackModel = v);
                  },
                ),
                const SizedBox(height: 14),
                _ModelDropdown(
                  label: '深度研判 / 追问模型 (Strong Model · 推荐 Pro / Max 旗舰档)',
                  value: _strongModel,
                  items: availableModels,
                  onChanged: (v) {
                    if (v != null) setState(() => _strongModel = v);
                  },
                ),
                const SizedBox(height: 16),
                FilledButton.icon(
                  onPressed: _savingModels ? null : _saveModelSettings,
                  icon: const Icon(Icons.cloud_done_rounded, size: 18),
                  label: Text(_savingModels ? '正在同步到服务端…' : '保存并立即同步生效'),
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 22),

        // 4. Connection & Local Device Push Settings (Win11 Tray + Android KeepAlive + Quiet Hours)
        Text('服务端连接与本机通知引擎', style: Theme.of(context).textTheme.titleLarge),
        const SizedBox(height: 10),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(18),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                TextField(
                  controller: _urlCtrl,
                  decoration: const InputDecoration(
                    labelText: '服务端域名地址 (Cloudflare Tunnel)',
                    hintText: 'https://intel.example.com',
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _tokenCtrl,
                  obscureText: true,
                  decoration: const InputDecoration(labelText: 'API Bearer Token (INTEL_API_TOKEN)'),
                ),
                const SizedBox(height: 12),
                Row(
                  children: [
                    Text('本设备弹窗最低评分：$_minPushScore 分', style: Theme.of(context).textTheme.labelLarge),
                    Expanded(
                      child: Slider(
                        min: 5,
                        max: 10,
                        divisions: 5,
                        value: _minPushScore.toDouble(),
                        onChanged: (v) => setState(() => _minPushScore = v.round()),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 6),
                Wrap(
                  spacing: 8,
                  runSpacing: 8,
                  children: [
                    for (final cat in const [
                      ('finance', '本机提醒:金融'),
                      ('ai', '本机提醒:AI'),
                      ('bigtech', '本机提醒:大厂'),
                      ('world', '本机提醒:全球'),
                    ])
                      _CompactPill(
                        label: cat.$2,
                        selected: _localNotifyCategories.contains(cat.$1),
                        onTap: () => setState(() {
                          if (_localNotifyCategories.contains(cat.$1)) {
                            if (_localNotifyCategories.length > 1) _localNotifyCategories.remove(cat.$1);
                          } else {
                            _localNotifyCategories.add(cat.$1);
                          }
                        }),
                      ),
                  ],
                ),
                const Divider(height: 24),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('本机夜间免打扰时段'),
                  subtitle: const Text('在指定时段内静音常规突发弹窗'),
                  value: _quietHoursEnabled,
                  onChanged: (v) => setState(() => _quietHoursEnabled = v),
                ),
                if (_quietHoursEnabled) ...[
                  Row(
                    children: [
                      Expanded(
                        child: TextField(
                          controller: _quietCtrl,
                          decoration: const InputDecoration(labelText: '免打扰时段 (HH:mm-HH:mm)', hintText: '00:30-07:30'),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: SwitchListTile(
                          contentPadding: EdgeInsets.zero,
                          title: const Text('9-10分特急突破免打扰', style: TextStyle(fontSize: 13)),
                          value: _criticalBypassQuiet,
                          onChanged: (v) => setState(() => _criticalBypassQuiet = v),
                        ),
                      ),
                    ],
                  ),
                ],
                if (Platform.isWindows)
                  SwitchListTile(
                    contentPadding: EdgeInsets.zero,
                    title: const Text('关闭窗口时最小化到系统托盘常驻'),
                    subtitle: const Text('保持 WebSocket 长连接与 2 分钟兜底同步，确保桌面端 24 小时秒收通知'),
                    value: _winMinimizeToTray,
                    onChanged: (v) => setState(() => _winMinimizeToTray = v),
                  ),
                if (Platform.isAndroid)
                  SwitchListTile(
                    contentPadding: EdgeInsets.zero,
                    title: const Text('ColorOS 后台常驻守护通知'),
                    subtitle: const Text('在通知栏常驻静默守护卡片 + 每 2 分钟兜底同步，防息屏漏报'),
                    value: _androidKeepAlive,
                    onChanged: (v) async {
                      setState(() => _androidKeepAlive = v);
                      final prefs = await SharedPreferences.getInstance();
                      await prefs.setBool('android_keep_alive', v);
                      if (v) {
                        await NativeNotifier.instance.startAndroidKeepAlive('实时在线 · 突发秒级监测中');
                      } else {
                        await NativeNotifier.instance.stopAndroidKeepAlive();
                      }
                    },
                  ),
                const SizedBox(height: 10),
                Wrap(
                  spacing: 10,
                  runSpacing: 10,
                  children: [
                    FilledButton.icon(
                      onPressed: _saveSettings,
                      icon: const Icon(Icons.save_rounded, size: 18),
                      label: const Text('保存并重连'),
                    ),
                    OutlinedButton.icon(
                      onPressed: _fireTestNotification,
                      icon: const Icon(Icons.notifications_active_rounded, size: 18),
                      label: const Text('测试原生系统通知'),
                    ),
                    if (Platform.isAndroid) ...[
                      OutlinedButton.icon(
                        onPressed: () => NativeNotifier.instance.openAndroidNotificationSettings(),
                        icon: const Icon(Icons.settings_applications_rounded, size: 18),
                        label: const Text('ColorOS 通知权限'),
                      ),
                      OutlinedButton.icon(
                        onPressed: () => NativeNotifier.instance.openAndroidBatterySettings(),
                        icon: const Icon(Icons.battery_charging_full_rounded, size: 18),
                        label: const Text('允许后台高灵敏运行'),
                      ),
                    ],
                  ],
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 22),

        // 5. Auto-Updater
        Text('版本检查与在线更新', style: Theme.of(context).textTheme.titleLarge),
        const SizedBox(height: 10),
        Card(
          child: Padding(
            padding: const EdgeInsets.all(18),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text('当前版本：v$kAppVersion', style: Theme.of(context).textTheme.titleSmall),
                          const SizedBox(height: 2),
                          Text(
                            '更新源：github.com/${_repoCtrl.text.trim().isNotEmpty ? _repoCtrl.text.trim() : kDefaultGithubRepo}',
                            style: Theme.of(context).textTheme.bodySmall,
                          ),
                        ],
                      ),
                    ),
                    FilledButton.tonalIcon(
                      onPressed: _checkingUpdate ? null : _checkForUpdates,
                      icon: _checkingUpdate
                          ? const SizedBox(width: 14, height: 14, child: CircularProgressIndicator(strokeWidth: 2))
                          : const Icon(Icons.system_update_alt_rounded, size: 18),
                      label: Text(_checkingUpdate ? '检查中…' : '检查更新'),
                    ),
                  ],
                ),
                const SizedBox(height: 6),
                ExpansionTile(
                  tilePadding: EdgeInsets.zero,
                  shape: const Border(),
                  title: Text('高级：自定义 Fork 仓库（默认无需修改）', style: Theme.of(context).textTheme.bodySmall),
                  children: [
                    const SizedBox(height: 6),
                    TextField(
                      controller: _repoCtrl,
                      decoration: const InputDecoration(labelText: 'GitHub 仓库 (owner/repo)', hintText: kDefaultGithubRepo),
                    ),
                    const SizedBox(height: 10),
                    TextField(
                      controller: _ghTokenCtrl,
                      obscureText: true,
                      decoration: const InputDecoration(labelText: 'GitHub Token（公开仓库留空即可）'),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
        const SizedBox(height: 22),

        // 6. Runtime Metrics & Budget Ledger
        if (ov != null) ...[
          Text('运行指标与 \$3/月 API 熔断账本', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 10),
          Card(
            child: Padding(
              padding: const EdgeInsets.all(18),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Wrap(
                    spacing: 28,
                    runSpacing: 14,
                    children: [
                      _MetricTile(label: '永久历史归档', value: '${ov.totalEvents} 条'),
                      _MetricTile(label: '24h P1 突发', value: '${ov.p1Events24h} 条'),
                      _MetricTile(label: '已收藏事件', value: '${ov.bookmarkedEvents} 条'),
                      _MetricTile(label: 'SQLite 体积', value: '${(ov.dbSizeBytes / 1024 / 1024).toStringAsFixed(2)} MB'),
                    ],
                  ),
                  const SizedBox(height: 18),
                  Text(
                    '当月 (${ov.budget.monthKey}) API 消费：\$${ov.budget.totalUsd.toStringAsFixed(4)} / \$${ov.budget.monthlyCapUsd.toStringAsFixed(2)} ${ov.budget.tripped ? "(已熔断降级至免费源)" : "(正常)"}',
                    style: Theme.of(context).textTheme.bodyMedium,
                  ),
                  const SizedBox(height: 8),
                  LinearProgressIndicator(
                    value: budgetPct,
                    minHeight: 8,
                    borderRadius: BorderRadius.circular(999),
                    color: ov.budget.tripped ? cs.error : const Color(0xFF10B981),
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 16),
          Text('信源健康监控 (${ov.sources.length})', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          Card(
            child: Column(
              children: [
                for (final s in ov.sources)
                  ListTile(
                    dense: true,
                    leading: Icon(
                      Icons.circle,
                      size: 10,
                      color: s.consecutiveFails == 0 ? const Color(0xFF10B981) : cs.error,
                    ),
                    title: Text(s.sourceName),
                    subtitle: Text(
                      s.consecutiveFails > 0 ? '连续失败 ${s.consecutiveFails} 次：${s.lastErrorMsg}' : s.sourceType,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
              ],
            ),
          ),
        ],
      ],
    );
  }
}

class _TitleBarIconBtn extends StatefulWidget {
  final IconData icon;
  final double iconSize;
  final Color color;
  final bool isClose;
  final VoidCallback onTap;

  const _TitleBarIconBtn({
    required this.icon,
    this.iconSize = 17,
    required this.color,
    this.isClose = false,
    required this.onTap,
  });

  @override
  State<_TitleBarIconBtn> createState() => _TitleBarIconBtnState();
}

class _TitleBarIconBtnState extends State<_TitleBarIconBtn> {
  bool _hovered = false;

  @override
  Widget build(BuildContext context) {
    final bg = _hovered
        ? (widget.isClose ? const Color(0xFFE81123) : widget.color.withValues(alpha: 0.1))
        : Colors.transparent;
    final fg = (_hovered && widget.isClose) ? Colors.white : widget.color;

    return MouseRegion(
      onEnter: (_) => setState(() => _hovered = true),
      onExit: (_) => setState(() => _hovered = false),
      child: GestureDetector(
        onTap: widget.onTap,
        child: Container(
          width: 44,
          height: 42,
          color: bg,
          alignment: Alignment.center,
          child: Icon(widget.icon, size: widget.iconSize, color: fg),
        ),
      ),
    );
  }
}

class _RailItem extends StatelessWidget {
  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback onTap;

  const _RailItem({required this.icon, required this.label, required this.selected, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      child: InkWell(
        borderRadius: BorderRadius.circular(14),
        onTap: onTap,
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(vertical: 10),
          decoration: BoxDecoration(
            color: selected ? cs.primary.withValues(alpha: 0.14) : Colors.transparent,
            borderRadius: BorderRadius.circular(14),
          ),
          child: Column(
            children: [
              Icon(icon, size: 21, color: selected ? cs.primary : cs.onSurface.withValues(alpha: 0.65)),
              const SizedBox(height: 4),
              Text(
                label,
                style: TextStyle(
                  fontSize: 11.5,
                  fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                  color: selected ? cs.primary : cs.onSurface.withValues(alpha: 0.65),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _CompactPill extends StatelessWidget {
  final String label;
  final IconData? icon;
  final bool selected;
  final VoidCallback onTap;

  const _CompactPill({required this.label, this.icon, required this.selected, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    return InkWell(
      borderRadius: BorderRadius.circular(999),
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: selected ? cs.primary.withValues(alpha: 0.14) : cs.surface,
          borderRadius: BorderRadius.circular(999),
          border: Border.all(color: selected ? cs.primary : cs.outline, width: 1),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (icon != null) ...[
              Icon(icon, size: 14, color: selected ? cs.primary : cs.onSurface.withValues(alpha: 0.7)),
              const SizedBox(width: 5),
            ],
            Text(
              label,
              style: TextStyle(
                fontSize: 12.5,
                fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                color: selected ? cs.primary : cs.onSurface,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ModelDropdown extends StatelessWidget {
  final String label;
  final String value;
  final List<String> items;
  final ValueChanged<String?> onChanged;

  const _ModelDropdown({required this.label, required this.value, required this.items, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    final safeValue = items.contains(value) ? value : (items.isNotEmpty ? items.first : null);
    return DropdownButtonFormField<String>(
      initialValue: safeValue,
      isExpanded: true,
      decoration: InputDecoration(labelText: label),
      items: items.map((m) => DropdownMenuItem(value: m, child: Text(m, overflow: TextOverflow.ellipsis))).toList(),
      onChanged: onChanged,
    );
  }
}

/// High-FPS lightweight card (no offscreen BoxShadow blur, cached static DateFormat, RepaintBoundary-friendly).
class _EventCard extends StatelessWidget {
  final IntelEvent event;
  final bool selected;
  final VoidCallback onTap;
  final VoidCallback onBookmark;

  const _EventCard({
    required this.event,
    required this.selected,
    required this.onTap,
    required this.onBookmark,
  });

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final dt = _fmtShort.format(DateTime.fromMillisecondsSinceEpoch(event.publishedAt * 1000));

    return Padding(
      padding: const EdgeInsets.only(bottom: 10),
      child: Material(
        color: selected ? cs.primary.withValues(alpha: isDark ? 0.14 : 0.06) : cs.surface,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(18),
          side: BorderSide(
            color: selected
                ? cs.primary
                : (event.score >= 9 ? cs.error.withValues(alpha: 0.45) : cs.outline),
            width: selected ? 1.5 : 1,
          ),
        ),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          child: Padding(
            padding: const EdgeInsets.all(15),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    _ScoreBadge(score: event.score),
                    const SizedBox(width: 8),
                    Text(event.categoryLabel, style: Theme.of(context).textTheme.labelSmall),
                    const Spacer(),
                    Text(dt, style: Theme.of(context).textTheme.labelSmall),
                    const SizedBox(width: 6),
                    InkWell(
                      onTap: onBookmark,
                      child: Icon(
                        event.isBookmarked ? Icons.bookmark_rounded : Icons.bookmark_border_rounded,
                        size: 16,
                        color: event.isBookmarked ? const Color(0xFFF59E0B) : cs.onSurface.withValues(alpha: 0.4),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Text(event.zhTitle, style: Theme.of(context).textTheme.titleMedium),
                if (event.impactAnalysis.isNotEmpty) ...[
                  const SizedBox(height: 6),
                  Text(
                    event.impactAnalysis,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _ScoreBadge extends StatelessWidget {
  final int score;
  const _ScoreBadge({required this.score});

  @override
  Widget build(BuildContext context) {
    final Color bg = score >= 9
        ? const Color(0xFFE11D48)
        : (score >= 8 ? const Color(0xFFD97706) : const Color(0xFF0066FF));
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(999)),
      child: Text('$score/10', style: const TextStyle(fontSize: 11, fontWeight: FontWeight.w600, color: Colors.white)),
    );
  }
}

class _ThreadBubble extends StatelessWidget {
  final EventThreadMsg msg;
  const _ThreadBubble({required this.msg});

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final isUser = msg.role == 'user';
    return Container(
      margin: const EdgeInsets.only(bottom: 10),
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: isUser ? cs.primary.withValues(alpha: 0.12) : cs.surface,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: isUser ? cs.primary.withValues(alpha: 0.4) : cs.outline),
      ),
      child: isUser
          ? SelectableText(msg.contentMd, style: Theme.of(context).textTheme.bodyMedium)
          : IntelMarkdownView(data: msg.contentMd),
    );
  }
}

class _MetricTile extends StatelessWidget {
  final String label;
  final String value;
  const _MetricTile({required this.label, required this.value});

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(label, style: Theme.of(context).textTheme.labelSmall),
        const SizedBox(height: 4),
        Text(value, style: Theme.of(context).textTheme.titleLarge),
      ],
    );
  }
}
