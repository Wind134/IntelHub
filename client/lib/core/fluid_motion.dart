import 'dart:io';
import 'dart:math' as math;

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

class FluidMotionScope extends InheritedWidget {
  final bool enabled;

  const FluidMotionScope({
    super.key,
    required this.enabled,
    required super.child,
  });

  static bool of(BuildContext context) {
    return context.dependOnInheritedWidgetOfExactType<FluidMotionScope>()?.enabled ?? true;
  }

  @override
  bool updateShouldNotify(FluidMotionScope oldWidget) => enabled != oldWidget.enabled;
}

ScrollPhysics fluidScrollPhysics(bool enabled, {bool alwaysScrollable = false}) {
  final base = enabled
      ? const BouncingScrollPhysics(decelerationRate: ScrollDecelerationRate.fast)
      : const ClampingScrollPhysics();
  return alwaysScrollable ? AlwaysScrollableScrollPhysics(parent: base) : base;
}

/// Windows 高刷惯性滚轮容器：基于 vsync Ticker 的指数阻尼插值，支持连续拨轮叠加动量，无 animateTo 重启卡顿。
class FluidScrollArea extends StatefulWidget {
  final ScrollController controller;
  final Widget child;

  const FluidScrollArea({
    super.key,
    required this.controller,
    required this.child,
  });

  @override
  State<FluidScrollArea> createState() => _FluidScrollAreaState();
}

class _FluidScrollAreaState extends State<FluidScrollArea> with SingleTickerProviderStateMixin {
  late final Ticker _ticker;
  double _targetOffset = 0;
  Duration _lastTick = Duration.zero;
  DateTime _lastWheelAt = DateTime.fromMillisecondsSinceEpoch(0);
  double _streakBoost = 1.0;

  @override
  void initState() {
    super.initState();
    _ticker = createTicker(_onTick);
  }

  @override
  void dispose() {
    _ticker.dispose();
    super.dispose();
  }

  void _stopMomentum() {
    if (_ticker.isActive) {
      _ticker.stop();
    }
    _lastTick = Duration.zero;
    _streakBoost = 1.0;
  }

  void _onTick(Duration elapsed) {
    if (!mounted || !widget.controller.hasClients) {
      _stopMomentum();
      return;
    }
    final pos = widget.controller.position;
    final dt = _lastTick == Duration.zero
        ? (1.0 / 120.0)
        : ((elapsed - _lastTick).inMicroseconds / 1000000.0).clamp(0.001, 0.05);
    _lastTick = elapsed;

    final clampedTarget = _targetOffset.clamp(pos.minScrollExtent, pos.maxScrollExtent);
    _targetOffset = clampedTarget;
    final diff = clampedTarget - pos.pixels;

    if (diff.abs() < 0.35) {
      if ((pos.pixels - clampedTarget).abs() > 0.01) {
        widget.controller.jumpTo(clampedTarget);
      }
      _stopMomentum();
      return;
    }

    // 帧率无关指数衰减（9.2 阻尼系数提供类似移动端惯性滑行收尾感）
    final factor = 1.0 - math.exp(-9.2 * dt);
    final next = (pos.pixels + diff * factor).clamp(pos.minScrollExtent, pos.maxScrollExtent);
    widget.controller.jumpTo(next);
  }

  void _handlePointerSignal(PointerSignalEvent event, bool fluidEnabled) {
    if (!fluidEnabled || !Platform.isWindows) return;
    if (event is! PointerScrollEvent || event.kind != PointerDeviceKind.mouse) return;
    if (!widget.controller.hasClients) return;

    final pos = widget.controller.position;
    final now = DateTime.now();
    final gapMs = now.difference(_lastWheelAt).inMilliseconds;
    _lastWheelAt = now;

    if (!_ticker.isActive || gapMs > 320) {
      _targetOffset = pos.pixels;
      _streakBoost = 1.0;
    } else if (gapMs < 130) {
      // 连续快速拨轮渐进增强惯性距离，上限 1.85 倍
      _streakBoost = (_streakBoost + 0.14).clamp(1.0, 1.85);
    }

    final delta = event.scrollDelta.dy * 1.42 * _streakBoost;
    _targetOffset = (_targetOffset + delta).clamp(pos.minScrollExtent, pos.maxScrollExtent);

    if (!_ticker.isActive) {
      _lastTick = Duration.zero;
      _ticker.start();
    }
  }

  @override
  Widget build(BuildContext context) {
    final enabled = FluidMotionScope.of(context);
    if (!Platform.isWindows || !enabled) {
      return widget.child;
    }
    return Listener(
      onPointerDown: (_) => _stopMomentum(),
      onPointerSignal: (ev) => _handlePointerSignal(ev, enabled),
      child: widget.child,
    );
  }
}

class FluidInteractiveCard extends StatefulWidget {
  final Widget child;
  final VoidCallback onTap;
  final bool animateEntrance;
  final int entranceDelayMs;

  const FluidInteractiveCard({
    super.key,
    required this.child,
    required this.onTap,
    this.animateEntrance = true,
    this.entranceDelayMs = 0,
  });

  @override
  State<FluidInteractiveCard> createState() => _FluidInteractiveCardState();
}

class _FluidInteractiveCardState extends State<FluidInteractiveCard> with SingleTickerProviderStateMixin {
  late final AnimationController _enterCtrl;
  late final Animation<double> _fadeAnim;
  late final Animation<Offset> _slideAnim;
  bool _pressed = false;
  bool _hovered = false;
  bool _started = false;

  @override
  void initState() {
    super.initState();
    _enterCtrl = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 260),
    );
    final curved = CurvedAnimation(parent: _enterCtrl, curve: Curves.easeOutCubic);
    _fadeAnim = Tween<double>(begin: 0.0, end: 1.0).animate(curved);
    _slideAnim = Tween<Offset>(begin: const Offset(0, 0.045), end: Offset.zero).animate(curved);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_started) {
      _started = true;
      final fluid = FluidMotionScope.of(context);
      if (!fluid || !widget.animateEntrance) {
        _enterCtrl.value = 1.0;
      } else if (widget.entranceDelayMs > 0) {
        Future.delayed(Duration(milliseconds: widget.entranceDelayMs), () {
          if (mounted) _enterCtrl.forward();
        });
      } else {
        _enterCtrl.forward();
      }
    }
  }

  @override
  void dispose() {
    _enterCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final fluid = FluidMotionScope.of(context);
    if (!fluid) return widget.child;

    final scale = _pressed ? 0.985 : (_hovered ? 1.006 : 1.0);
    return FadeTransition(
      opacity: _fadeAnim,
      child: SlideTransition(
        position: _slideAnim,
        child: MouseRegion(
          onEnter: (_) => setState(() => _hovered = true),
          onExit: (_) => setState(() => _hovered = false),
          child: Listener(
            onPointerDown: (_) => setState(() => _pressed = true),
            onPointerUp: (_) => setState(() => _pressed = false),
            onPointerCancel: (_) => setState(() => _pressed = false),
            child: AnimatedScale(
              scale: scale,
              duration: const Duration(milliseconds: 140),
              curve: Curves.easeOutCubic,
              child: widget.child,
            ),
          ),
        ),
      ),
    );
  }
}

class FluidPageRoute<T> extends PageRouteBuilder<T> {
  FluidPageRoute({
    required WidgetBuilder builder,
    bool fluid = true,
  }) : super(
          transitionDuration: Duration(milliseconds: fluid ? 280 : 140),
          reverseTransitionDuration: Duration(milliseconds: fluid ? 240 : 120),
          pageBuilder: (context, animation, secondaryAnimation) => builder(context),
          transitionsBuilder: (context, animation, secondaryAnimation, child) {
            if (!fluid) return child;
            final curved = CurvedAnimation(
              parent: animation,
              curve: Curves.easeOutCubic,
              reverseCurve: Curves.easeInCubic,
            );
            return FadeTransition(
              opacity: curved,
              child: SlideTransition(
                position: Tween<Offset>(begin: const Offset(0.08, 0), end: Offset.zero).animate(curved),
                child: ScaleTransition(
                  scale: Tween<double>(begin: 0.98, end: 1.0).animate(curved),
                  child: child,
                ),
              ),
            );
          },
        );
}

/// 保留各 Tab 滚动状态的同时提供方向感切入过渡动画（桌面纵向随侧栏上下，移动端横向随底栏左右）。
class FluidIndexedStack extends StatefulWidget {
  final int index;
  final List<Widget> children;
  final Axis axis;

  const FluidIndexedStack({
    super.key,
    required this.index,
    required this.children,
    this.axis = Axis.horizontal,
  });

  @override
  State<FluidIndexedStack> createState() => _FluidIndexedStackState();
}

class _FluidIndexedStackState extends State<FluidIndexedStack> with SingleTickerProviderStateMixin {
  late final AnimationController _ctrl;
  late int _currentIndex;
  int? _previousIndex;
  int _direction = 1;

  @override
  void initState() {
    super.initState();
    _currentIndex = widget.index;
    _ctrl = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 260),
      value: 1.0,
    )..addStatusListener((st) {
        if (st == AnimationStatus.completed && mounted && _previousIndex != null) {
          setState(() => _previousIndex = null);
        }
      });
  }

  @override
  void didUpdateWidget(covariant FluidIndexedStack oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.index != _currentIndex) {
      _direction = widget.index > _currentIndex ? 1 : -1;
      _previousIndex = _currentIndex;
      _currentIndex = widget.index;
      final fluid = FluidMotionScope.of(context);
      if (fluid) {
        _ctrl.forward(from: 0.0);
      } else {
        _previousIndex = null;
        _ctrl.value = 1.0;
      }
    }
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final fluid = FluidMotionScope.of(context);
    if (!fluid) {
      return IndexedStack(index: widget.index, children: widget.children);
    }

    return AnimatedBuilder(
      animation: _ctrl,
      builder: (_, __) {
        final t = Curves.easeOutCubic.transform(_ctrl.value);
        final inOffset = widget.axis == Axis.horizontal
            ? Offset((1.0 - t) * 0.055 * _direction, 0)
            : Offset(0, (1.0 - t) * 0.035 * _direction);
        final outOffset = widget.axis == Axis.horizontal
            ? Offset(-t * 0.03 * _direction, 0)
            : Offset(0, -t * 0.02 * _direction);
        final inScale = 0.986 + 0.014 * t;

        return Stack(
          fit: StackFit.expand,
          children: [
            for (var i = 0; i < widget.children.length; i++)
              if (i == _currentIndex)
                Opacity(
                  opacity: t.clamp(0.0, 1.0),
                  child: FractionalTranslation(
                    translation: inOffset,
                    child: Transform.scale(
                      scale: inScale,
                      child: widget.children[i],
                    ),
                  ),
                )
              else if (i == _previousIndex && _ctrl.isAnimating)
                IgnorePointer(
                  child: Opacity(
                    opacity: (1.0 - t).clamp(0.0, 1.0),
                    child: FractionalTranslation(
                      translation: outOffset,
                      child: widget.children[i],
                    ),
                  ),
                )
              else
                Offstage(offstage: true, child: TickerMode(enabled: false, child: widget.children[i])),
          ],
        );
      },
    );
  }
}
