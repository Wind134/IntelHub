import 'dart:io';

import 'package:flutter/material.dart';

import 'api_client.dart';

enum AuthMode { login, register, legacyToken }

class AuthResult {
  final String serverUrl;
  final String sessionToken;
  final String username;
  final bool isLegacy;
  final Map<String, dynamic> preferences;

  const AuthResult({
    required this.serverUrl,
    required this.sessionToken,
    required this.username,
    required this.isLegacy,
    required this.preferences,
  });
}

/// Clean Aquamorphic / Windows 11 Login & First-Time Account Registration view for informationHub.
class InformationHubAuthScreen extends StatefulWidget {
  final IntelApiClient api;
  final String initialServerUrl;
  final ValueChanged<AuthResult> onAuthenticated;

  const InformationHubAuthScreen({
    super.key,
    required this.api,
    required this.initialServerUrl,
    required this.onAuthenticated,
  });

  @override
  State<InformationHubAuthScreen> createState() => _InformationHubAuthScreenState();
}

class _InformationHubAuthScreenState extends State<InformationHubAuthScreen> {
  final TextEditingController _serverCtrl = TextEditingController();
  final TextEditingController _userCtrl = TextEditingController();
  final TextEditingController _passCtrl = TextEditingController();
  final TextEditingController _setupKeyCtrl = TextEditingController();
  final TextEditingController _legacyTokenCtrl = TextEditingController();

  AuthMode _mode = AuthMode.login;
  bool _obscurePass = true;
  bool _submitting = false;
  bool _probed = false;
  String? _errorMsg;
  String? _serverHint;

  @override
  void initState() {
    super.initState();
    _serverCtrl.text = widget.initialServerUrl;
    if (_serverCtrl.text.trim().isNotEmpty) {
      _probeServerStatus();
    }
  }

  @override
  void dispose() {
    _serverCtrl.dispose();
    _userCtrl.dispose();
    _passCtrl.dispose();
    _setupKeyCtrl.dispose();
    _legacyTokenCtrl.dispose();
    super.dispose();
  }

  Future<void> _probeServerStatus() async {
    final url = _serverCtrl.text.trim();
    if (url.isEmpty) return;
    try {
      final st = await widget.api.fetchAuthStatus(url);
      if (!mounted) return;
      final requiresSetup = st['requires_setup'] == true;
      setState(() {
        _probed = true;
        if (requiresSetup) {
          _mode = AuthMode.register;
          _serverHint = '检测到服务端尚未创建账号，请使用一次服务器密钥创建专属加密账号';
        } else {
          _serverHint = '已连接服务端 · 请输入账号密码登录，自动同步云端加密配置';
        }
      });
    } catch (_) {}
  }

  Future<void> _submit() async {
    final serverUrl = _serverCtrl.text.trim().replaceAll(RegExp(r'/+$'), '');
    if (serverUrl.isEmpty || (!serverUrl.startsWith('http://') && !serverUrl.startsWith('https://'))) {
      setState(() => _errorMsg = '请先填写正确的服务端地址（以 https:// 开头）');
      return;
    }

    setState(() {
      _submitting = true;
      _errorMsg = null;
    });

    final platform = Platform.isAndroid ? 'android' : 'windows';
    final deviceName = Platform.isAndroid ? 'Android (ColorOS)' : 'Windows 11 Desktop';

    try {
      if (_mode == AuthMode.legacyToken) {
        final tok = _legacyTokenCtrl.text.trim();
        if (tok.isEmpty) throw const FormatException('请输入 API Bearer Token');
        widget.api.baseUrl = serverUrl;
        widget.api.apiToken = tok;
        await widget.api.fetchAuthMe();
        widget.onAuthenticated(AuthResult(
          serverUrl: serverUrl,
          sessionToken: tok,
          username: 'API Token 模式',
          isLegacy: true,
          preferences: const {},
        ));
        return;
      }

      final username = _userCtrl.text.trim();
      final password = _passCtrl.text;
      if (username.length < 2) throw const FormatException('用户名至少需要 2 个字符');
      if (password.length < 8) throw const FormatException('密码长度至少 8 位');

      Map<String, dynamic> res;
      if (_mode == AuthMode.register) {
        final setupKey = _setupKeyCtrl.text.trim();
        if (setupKey.isEmpty) throw const FormatException('创建账号需要提供一次服务器密钥（INTEL_API_TOKEN）');
        res = await widget.api.registerAccount(
          serverUrl: serverUrl,
          username: username,
          password: password,
          setupKey: setupKey,
          deviceName: deviceName,
          platform: platform,
        );
      } else {
        res = await widget.api.loginAccount(
          serverUrl: serverUrl,
          username: username,
          password: password,
          deviceName: deviceName,
          platform: platform,
        );
      }

      final token = (res['token'] as String?) ?? '';
      final userMap = (res['user'] as Map<String, dynamic>?) ?? const {};
      final prefsMap = (res['preferences'] as Map<String, dynamic>?) ?? const {};
      final uname = (userMap['username'] as String?) ?? username;
      if (token.isEmpty) throw const HttpException('服务端未返回有效登录凭证');

      widget.onAuthenticated(AuthResult(
        serverUrl: serverUrl,
        sessionToken: token,
        username: uname,
        isLegacy: false,
        preferences: prefsMap,
      ));
    } catch (e) {
      if (mounted) {
        setState(() => _errorMsg = e.toString().replaceFirst('HttpException: ', '').replaceFirst('FormatException: ', ''));
      }
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return Center(
      child: SingleChildScrollView(
        padding: const EdgeInsets.all(24),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 440),
          child: Card(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(26, 28, 26, 24),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Row(
                    children: [
                      ClipRRect(
                        borderRadius: BorderRadius.circular(14),
                        child: Image.asset(
                          'assets/app_icon_squircle.png',
                          width: 48,
                          height: 48,
                          filterQuality: FilterQuality.high,
                        ),
                      ),
                      const SizedBox(width: 14),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text('informationHub', style: Theme.of(context).textTheme.headlineSmall),
                            const SizedBox(height: 2),
                            Text(
                              '账号加密认证 · 跨设备配置云端绑定',
                              style: Theme.of(context).textTheme.bodySmall,
                            ),
                          ],
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 20),
                  SegmentedButton<AuthMode>(
                    segments: const [
                      ButtonSegment(value: AuthMode.login, label: Text('账号登录')),
                      ButtonSegment(value: AuthMode.register, label: Text('创建账号')),
                    ],
                    selected: {_mode == AuthMode.register ? AuthMode.register : AuthMode.login},
                    onSelectionChanged: (s) => setState(() {
                      _mode = s.first;
                      _errorMsg = null;
                    }),
                  ),
                  const SizedBox(height: 18),
                  TextField(
                    controller: _serverCtrl,
                    onEditingComplete: _probeServerStatus,
                    decoration: InputDecoration(
                      labelText: '服务端地址',
                      hintText: 'https://your-domain.example.com',
                      prefixIcon: const Icon(Icons.dns_outlined, size: 19),
                      suffixIcon: IconButton(
                        tooltip: '检测服务端状态',
                        icon: Icon(_probed ? Icons.verified_rounded : Icons.sync_rounded,
                            size: 18, color: _probed ? const Color(0xFF10B981) : null),
                        onPressed: _probeServerStatus,
                      ),
                    ),
                  ),
                  if (_serverHint != null) ...[
                    const SizedBox(height: 6),
                    Text(_serverHint!, style: TextStyle(fontSize: 11.5, color: cs.primary)),
                  ],
                  const SizedBox(height: 12),
                  if (_mode == AuthMode.legacyToken) ...[
                    TextField(
                      controller: _legacyTokenCtrl,
                      obscureText: true,
                      onSubmitted: (_) => _submit(),
                      decoration: const InputDecoration(
                        labelText: '传统 API Bearer Token (INTEL_API_TOKEN)',
                        prefixIcon: Icon(Icons.vpn_key_outlined, size: 19),
                      ),
                    ),
                  ] else ...[
                    TextField(
                      controller: _userCtrl,
                      textInputAction: TextInputAction.next,
                      decoration: const InputDecoration(
                        labelText: '用户名',
                        hintText: '如 admin / 你的昵称',
                        prefixIcon: Icon(Icons.person_outline_rounded, size: 19),
                      ),
                    ),
                    const SizedBox(height: 12),
                    TextField(
                      controller: _passCtrl,
                      obscureText: _obscurePass,
                      textInputAction: _mode == AuthMode.register ? TextInputAction.next : TextInputAction.done,
                      onSubmitted: _mode == AuthMode.login ? (_) => _submit() : null,
                      decoration: InputDecoration(
                        labelText: '密码（至少 8 位）',
                        prefixIcon: const Icon(Icons.lock_outline_rounded, size: 19),
                        suffixIcon: IconButton(
                          icon: Icon(_obscurePass ? Icons.visibility_outlined : Icons.visibility_off_outlined, size: 18),
                          onPressed: () => setState(() => _obscurePass = !_obscurePass),
                        ),
                      ),
                    ),
                    if (_mode == AuthMode.register) ...[
                      const SizedBox(height: 12),
                      TextField(
                        controller: _setupKeyCtrl,
                        obscureText: true,
                        onSubmitted: (_) => _submit(),
                        decoration: const InputDecoration(
                          labelText: '服务器初始化密钥 (INTEL_API_TOKEN)',
                          helperText: '仅首次建号校验一次，之后所有设备只需账号密码登录',
                          prefixIcon: Icon(Icons.shield_outlined, size: 19),
                        ),
                      ),
                    ],
                  ],
                  if (_errorMsg != null) ...[
                    const SizedBox(height: 12),
                    Container(
                      padding: const EdgeInsets.all(10),
                      decoration: BoxDecoration(
                        color: cs.error.withValues(alpha: 0.1),
                        borderRadius: BorderRadius.circular(12),
                        border: Border.all(color: cs.error.withValues(alpha: 0.35)),
                      ),
                      child: Text(_errorMsg!, style: TextStyle(color: cs.error, fontSize: 12.5)),
                    ),
                  ],
                  const SizedBox(height: 18),
                  FilledButton.icon(
                    onPressed: _submitting ? null : _submit,
                    style: FilledButton.styleFrom(padding: const EdgeInsets.symmetric(vertical: 13)),
                    icon: _submitting
                        ? const SizedBox(
                            width: 16,
                            height: 16,
                            child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                          )
                        : Icon(_mode == AuthMode.register ? Icons.person_add_alt_1_rounded : Icons.login_rounded, size: 18),
                    label: Text(
                      _submitting
                          ? '安全认证中…'
                          : switch (_mode) {
                              AuthMode.register => '创建加密账号并登录',
                              AuthMode.legacyToken => '使用 Token 直连',
                              AuthMode.login => '加密登录并同步配置',
                            },
                    ),
                  ),
                  const SizedBox(height: 12),
                  Container(
                    padding: const EdgeInsets.all(10),
                    decoration: BoxDecoration(
                      color: cs.primary.withValues(alpha: isDark ? 0.1 : 0.05),
                      borderRadius: BorderRadius.circular(12),
                    ),
                    child: Row(
                      children: [
                        Icon(Icons.verified_user_outlined, size: 15, color: cs.primary),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Text(
                            '密码经 PBKDF2-SHA256 哈希；云端个人配置采用 AES-256-GCM 加密存储；本机凭据由 '
                            '${Platform.isAndroid ? "Android Keystore" : "Windows DPAPI"} 硬件级保护。',
                            style: Theme.of(context).textTheme.labelSmall,
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(height: 6),
                  Align(
                    alignment: Alignment.center,
                    child: TextButton(
                      onPressed: () => setState(() {
                        _mode = _mode == AuthMode.legacyToken ? AuthMode.login : AuthMode.legacyToken;
                        _errorMsg = null;
                      }),
                      child: Text(
                        _mode == AuthMode.legacyToken ? '返回账号密码登录' : '兼容模式：使用旧版 API Token 直连',
                        style: Theme.of(context).textTheme.labelMedium,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
