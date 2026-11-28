import 'package:flutter_test/flutter_test.dart';
import 'package:intel_hub_client/core/summary_reader.dart';

void main() {
  test('splits legacy Chinese wall-of-text into readable paragraphs while preserving decimals', () {
    const sample =
        'IT之家 11 月 28 日消息，科技媒体 Windows Latest 昨日发布博文，报道称微软旗下代码托管平台 GitHub 陷入舆论漩涡。'
        'AnyPS5 是 GitHub 上的开源项目，目标是把 PS5 游戏的可执行文件重写成能在 Windows 和 Linux 上运行的程序。'
        '该项目于今年 8 月发布，IT之家发稿前访问已 Fork 超 6,100 个，Star 数量达到 6.28 万个。'
        '根据项目动态，AnyPS5 已实现 18824/39519 个系统库函数，已实现函数占已声明函数的比例达到 47.63%，完成度达到 100%。'
        '微软 GitHub 于 11 月 24 日应游戏发行商 Take-Two 的要求，关闭了名为 Leonida 的《GTA 6》PC 移植项目网络。';

    final paras = splitIntoReadableParagraphs(sample);
    expect(paras.length, greaterThanOrEqualTo(2));
    expect(paras.join(''), contains('47.63%'));
    expect(paras.join(''), contains('6.28 万个'));
  });

  test('preserves explicit server-side paragraph breaks', () {
    const sample = '第一段摘要内容。\n\n第二段摘要内容。';
    final paras = splitIntoReadableParagraphs(sample);
    expect(paras, ['第一段摘要内容。', '第二段摘要内容。']);
  });
}
