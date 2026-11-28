import 'models.dart';

/// 优先使用服务端 AI 裁判直出的内容定制追问标签；旧归档记录无 AI 标签时回退至精细主题规则。
List<String> buildSmartFollowUpPrompts(IntelEvent ev) {
  final aiPrompts = ev.followUps
      .map((e) => e.trim())
      .where((e) => e.isNotEmpty)
      .toList();
  if (aiPrompts.isNotEmpty) {
    return aiPrompts.take(3).toList();
  }

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
  // 4. 航空 / 交通 / 安全事故与监管调查
  else if (_hasAny(text, ['客机', '航班', '机场', '波音', '空客', 'faa', '擦碰', '停飞', '事故', '空管'])) {
    if (e1.isNotEmpty && e2.isNotEmpty) {
      addPrompt('复盘 $e1 与 $e2 近期同类安全事件及监管审查重点');
    } else if (e1.isNotEmpty) {
      addPrompt('分析本次事件对 $e1 运力调度、口碑及安全审查的影响');
    }
    addPrompt('梳理 FAA 等监管机构可能采取的后续调查与整改措施');
    addPrompt('核查涉事机场或机型近年安全记录与关键隐患');
  }
  // 5. 网络安全 / 漏洞 / 宕机事故
  else if (_hasAny(text, ['漏洞', 'cve', 'cvss', '0-day', 'zero-day', '宕机', '中断', '攻击', '勒索'])) {
    if (e1.isNotEmpty) {
      addPrompt('梳理 $e1 该漏洞/故障的受影响范围与紧急缓解方案');
    }
    addPrompt('评估对云服务商与企业级客户的业务连续性风险');
    addPrompt('总结官方补丁进度与后续需要监控的安全信号');
  }
  // 6. AI 大模型 / 算力 / 芯片
  else if (ev.category == 'ai' || _hasAny(text, ['大模型', '模型', '开源', '算力', 'gpu', '芯片', 'openai', 'claude', 'deepseek', 'gemini', 'nvidia'])) {
    if (e1.isNotEmpty) {
      addPrompt('深度对比 $e1 此次进展与主流竞品的技术及成本差距');
    }
    addPrompt('评估对算力产业链（GPU/云厂商）与应用层的实际影响');
    addPrompt('判断商业化落地节奏及下一阶段关键催化节点');
  }
  // 7. 财报 / 并购 / 大厂战略
  else if (_hasAny(text, ['财报', '营收', '利润', '指引', '收购', '并购', '反垄断', '裁员', '股价', '异动'])) {
    if (e1.isNotEmpty) {
      addPrompt('拆解 $e1 此次事件的核心超预期点与隐含基本面变化');
    }
    addPrompt('梳理上下游受益与受损标的及产业链连锁反应');
    addPrompt('总结市场多空分歧焦点与后续跟踪指标');
  }
  // 8. 真正涉及地缘冲突/能源航运/制裁时才推送地缘大宗维度
  else if (_hasAny(text, ['冲突', '制裁', '停火', '战争', '军演', '封锁', '导弹', '原油', '航运', '选举', '外交'])) {
    if (e1.isNotEmpty) {
      addPrompt('推演 $e1 事态升级或缓和的三种可能情景');
    }
    addPrompt('评估对大宗商品（原油/黄金）与国际航运供应链的直接冲击');
    addPrompt('列出未来 72 小时最值得关注的外交与市场信号');
  }

  if (out.length < 3 && e1.isNotEmpty) {
    addPrompt('梳理「$e1」此事的来龙去脉与核心争议点');
  }
  if (out.length < 3) {
    addPrompt(
      ev.category == 'finance'
          ? '梳理受该消息影响最大的核心标的与交易逻辑'
          : '核查该事件的背景脉络与历史同类案例演变',
    );
  }
  if (out.length < 3) {
    addPrompt('总结后续最关键的进展节点与潜在风险');
  }
  return out.take(3).toList();
}

bool _hasAny(String text, List<String> keywords) {
  for (final kw in keywords) {
    if (text.contains(kw)) return true;
  }
  return false;
}
