import 'models.dart';

/// 根据事件标题、领域、核心实体与影响推演，零延迟生成 3 条高相关 AI 深度追问建议。
List<String> buildSmartFollowUpPrompts(IntelEvent ev) {
  final text = '${ev.zhTitle} ${ev.summary} ${ev.impactAnalysis}'.toLowerCase();
  final entities = ev.entities
      .map((e) => e.trim())
      .where((e) => e.isNotEmpty && e.length <= 16)
      .toList();
  final e1 = entities.isNotEmpty ? entities[0] : '';
  final e2 = entities.length > 1 ? entities[1] : '';

  final out = <String>[];
  void addPrompt(String s) {
    final cleaned = s.trim();
    if (cleaned.isNotEmpty && !out.contains(cleaned)) {
      out.add(cleaned);
    }
  }

  // 1. 银行/金融机构风险与并购接管
  if (_hasAny(text, ['银行', '倒闭', 'fdic', '挤兑', '存款保险', '破产', '接管'])) {
    if (e1.isNotEmpty && e2.isNotEmpty) {
      addPrompt('分析 $e2 介入 $e1 处置的财务影响与潜在风险');
    } else if (e1.isNotEmpty) {
      addPrompt('评估 $e1 风险事件对金融板块与流动性的冲击');
    }
    addPrompt('会否引发区域银行连锁挤兑与信用利差走阔？');
    addPrompt('对比 2023 硅谷银行危机，此次监管处置释放何种信号？');
  }
  // 2. A股国家队 ETF / 资金流向异动
  else if (_hasAny(text, ['etf', '净申购', '净赎回', '国家队', '放量', '宽基', '沪深300', '中证1000'])) {
    if (e1.isNotEmpty) {
      addPrompt('分析 $e1 此次资金异动的主力意图与持续性');
    }
    addPrompt('结合当前估值与成交量，研判对 A 股风格切换的影响');
    addPrompt('盘点盘后份额确认与后续需要盯紧的联动信号');
  }
  // 3. 央行利率 / 通胀 / 宏观政策
  else if (_hasAny(text, ['美联储', 'fed', '降息', '加息', 'cpi', 'pce', '非农', '央行', '国债', '关税'])) {
    addPrompt('推演此次宏观变量对美债收益率、美元与黄金的传导路径');
    addPrompt('梳理对科技成长股与高股息板块的估值再定价影响');
    addPrompt('对比市场此前预期，挖掘最容易被忽略的二阶风险');
  }
  // 4. AI 大模型 / 算力 / 芯片
  else if (ev.category == 'ai' || _hasAny(text, ['大模型', '模型', '开源', '算力', 'gpu', '芯片', 'openai', 'claude', 'deepseek', 'nvidia'])) {
    if (e1.isNotEmpty) {
      addPrompt('深度对比 $e1 此次进展与主流竞品的技术及成本差距');
    }
    addPrompt('评估对算力产业链（GPU/光模块/云厂商）与应用层的影响');
    addPrompt('判断商业化落地节奏及下一阶段关键催化节点');
  }
  // 5. 财报 / 并购 / 大厂战略
  else if (_hasAny(text, ['财报', '营收', '利润', '指引', '收购', '并购', '反垄断', '裁员', '股价', '异动'])) {
    if (e1.isNotEmpty) {
      addPrompt('拆解 $e1 此次事件的核心超预期点与隐含基本面变化');
    }
    addPrompt('梳理上下游受益与受损标的及产业链连锁反应');
    addPrompt('总结华尔街多空分歧焦点与后续跟踪指标');
  }
  // 6. 地缘冲突 / 国际突发
  else if (ev.category == 'world' || _hasAny(text, ['冲突', '制裁', '谈判', '停火', '战争', '原油', '航运', '选举'])) {
    if (e1.isNotEmpty) {
      addPrompt('推演 $e1 事态升级或缓和的三种可能情景');
    }
    addPrompt('评估对全球大宗商品（原油/黄金）与供应链的直接冲击');
    addPrompt('列出未来 72 小时最值得关注的外交与市场信号');
  }

  // 通用实体兜底补足至 3 条
  if (out.length < 3 && e1.isNotEmpty) {
    addPrompt('深入分析对 $e1 及上下游相关标的的实质影响');
  }
  if (out.length < 3) {
    addPrompt(
      ev.category == 'finance'
          ? '梳理受该消息影响最大的核心标的与交易逻辑'
          : '核查事件前因后果与历史同类事件演变路径',
    );
  }
  if (out.length < 3) {
    addPrompt('总结最核心的潜在风险点与下一步关键观察信号');
  }
  return out.take(3).toList();
}

bool _hasAny(String text, List<String> keywords) {
  for (final kw in keywords) {
    if (text.contains(kw)) return true;
  }
  return false;
}
