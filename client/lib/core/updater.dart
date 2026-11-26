import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;

const String kAppVersion = String.fromEnvironment('APP_VERSION', defaultValue: '1.0.0');
const String kDefaultGithubRepo = String.fromEnvironment('GITHUB_REPO', defaultValue: '');

class ReleaseInfo {
  final String tagName;
  final String version;
  final String name;
  final String bodyMd;
  final String htmlUrl;
  final String? assetName;
  final String? assetDownloadUrl;
  final int assetSizeBytes;
  final bool hasUpdate;

  const ReleaseInfo({
    required this.tagName,
    required this.version,
    required this.name,
    required this.bodyMd,
    required this.htmlUrl,
    required this.assetName,
    required this.assetDownloadUrl,
    required this.assetSizeBytes,
    required this.hasUpdate,
  });
}

class AppUpdater {
  static String normalizeVersion(String raw) {
    var v = raw.trim();
    if (v.startsWith('v') || v.startsWith('V')) {
      v = v.substring(1);
    }
    final plus = v.indexOf('+');
    if (plus >= 0) v = v.substring(0, plus);
    final dash = v.indexOf('-');
    if (dash >= 0) v = v.substring(0, dash);
    return v;
  }

  /// Returns true when remote SemVer is strictly newer than local SemVer.
  static bool isNewer(String remoteRaw, String localRaw) {
    final r = normalizeVersion(remoteRaw).split('.').map((e) => int.tryParse(e) ?? 0).toList();
    final l = normalizeVersion(localRaw).split('.').map((e) => int.tryParse(e) ?? 0).toList();
    final len = r.length > l.length ? r.length : l.length;
    for (var i = 0; i < len; i++) {
      final rv = i < r.length ? r[i] : 0;
      final lv = i < l.length ? l[i] : 0;
      if (rv != lv) return rv > lv;
    }
    return false;
  }

  static Future<ReleaseInfo> checkLatest({
    required String repoSlug,
    String githubToken = '',
  }) async {
    final slug = repoSlug.trim().replaceAll(RegExp(r'^https?://github\.com/'), '').replaceAll(RegExp(r'/+$'), '');
    if (!slug.contains('/')) {
      throw const FormatException('请填写格式为 owner/repo 的 GitHub 仓库名');
    }
    final uri = Uri.parse('https://api.github.com/repos/$slug/releases/latest');
    final headers = <String, String>{
      'Accept': 'application/vnd.github+json',
      'User-Agent': 'IntelHub-Client/$kAppVersion',
      if (githubToken.trim().isNotEmpty) 'Authorization': 'Bearer ${githubToken.trim()}',
    };
    final resp = await http.get(uri, headers: headers).timeout(const Duration(seconds: 12));
    if (resp.statusCode == 404) {
      throw const HttpException('尚未发布任何 GitHub Release（或私有仓库未填访问 Token）');
    }
    if (resp.statusCode != 200) {
      throw HttpException('GitHub API HTTP ${resp.statusCode}');
    }

    final data = jsonDecode(resp.body) as Map<String, dynamic>;
    final tag = (data['tag_name'] as String?) ?? '';
    final assets = (data['assets'] as List?) ?? const [];

    String? assetName;
    String? downloadUrl;
    int assetSize = 0;

    for (final a in assets) {
      if (a is! Map<String, dynamic>) continue;
      final name = (a['name'] as String?) ?? '';
      final lower = name.toLowerCase();
      final matched = Platform.isWindows
          ? (lower.endsWith('.exe') && lower.contains('setup')) || lower.endsWith('.exe')
          : Platform.isAndroid
              ? lower.endsWith('.apk')
              : false;
      if (matched) {
        assetName = name;
        // Private repos require downloading via API asset URL with Accept: application/octet-stream
        downloadUrl = githubToken.trim().isNotEmpty
            ? (a['url'] as String?) ?? (a['browser_download_url'] as String?)
            : (a['browser_download_url'] as String?);
        assetSize = (a['size'] as num?)?.toInt() ?? 0;
        break;
      }
    }

    return ReleaseInfo(
      tagName: tag,
      version: normalizeVersion(tag),
      name: (data['name'] as String?)?.isNotEmpty == true ? data['name'] as String : tag,
      bodyMd: (data['body'] as String?) ?? '包含常规体验优化与问题修复。',
      htmlUrl: (data['html_url'] as String?) ?? 'https://github.com/$slug/releases',
      assetName: assetName,
      assetDownloadUrl: downloadUrl,
      assetSizeBytes: assetSize,
      hasUpdate: isNewer(tag, kAppVersion),
    );
  }

  /// Downloads the Windows installer to %USERPROFILE%\Downloads and returns the saved file path.
  static Future<String> downloadWindowsInstaller({
    required ReleaseInfo release,
    String githubToken = '',
    required void Function(double progress) onProgress,
  }) async {
    final url = release.assetDownloadUrl;
    if (url == null || url.isEmpty) {
      throw const HttpException('当前版本未找到 Windows .exe 安装包');
    }
    final userProfile = Platform.environment['USERPROFILE'] ?? Directory.systemTemp.path;
    final downloadsDir = Directory('$userProfile${Platform.pathSeparator}Downloads');
    if (!downloadsDir.existsSync()) {
      downloadsDir.createSync(recursive: true);
    }
    final fileName = release.assetName ?? 'IntelHub-Setup-${release.tagName}-win64.exe';
    final destFile = File('${downloadsDir.path}${Platform.pathSeparator}$fileName');

    final client = http.Client();
    try {
      final req = http.Request('GET', Uri.parse(url));
      req.headers['User-Agent'] = 'IntelHub-Client/$kAppVersion';
      if (githubToken.trim().isNotEmpty) {
        req.headers['Authorization'] = 'Bearer ${githubToken.trim()}';
        req.headers['Accept'] = 'application/octet-stream';
      }
      final resp = await client.send(req);
      if (resp.statusCode >= 300) {
        throw HttpException('下载失败：HTTP ${resp.statusCode}');
      }
      final total = resp.contentLength ?? release.assetSizeBytes;
      var received = 0;
      final sink = destFile.openWrite();
      await for (final chunk in resp.stream) {
        sink.add(chunk);
        received += chunk.length;
        if (total > 0) {
          onProgress((received / total).clamp(0.0, 1.0));
        }
      }
      await sink.close();
      onProgress(1.0);
      return destFile.path;
    } finally {
      client.close();
    }
  }
}
