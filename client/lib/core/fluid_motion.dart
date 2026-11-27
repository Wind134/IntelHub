import 'dart:io';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

/// Inherited toggle for synchronized High-Frame-Rate & Fluid Motion mode across Windows & Android.
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

/// Returns platform-tuned high-FPS scroll physics:
/// - Fluid mode ON: buttery spring BouncingScrollPhysics (fast deceleration, ColorOS/iOS feel)
/// - Fluid mode OFF: standard ClampingScrollPhysics (power-saving / minimal motion)
ScrollPhysics fluidScrollPhysics(bool enabled, {bool alwaysScrollable = false}) {
  final base = enabled
      ? const BouncingScrollPhysics(decelerationRate: ScrollDecelerationRate.fast)
      : const ClampingScrollPhysics();
  return alwaysScrollable ? AlwaysScrollableScrollPhysics(parent: base) : base;
}

/// High-refresh-rate desktop mouse-wheel smooth scroller (120Hz / 144Hz / 240Hz):
/// Intercepts stepped mouse-wheel ticks on Windows and interpolates them smoothly with easeOutCubic
/// while leaving touch/trackpad gestures native and instant.
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

class _FluidScrollAreaState extends State<FluidScrollArea> {
  double _targetOffset = 0;
  DateTime _lastWheelAt = DateTime.fromMillisecondsSinceEpoch(0);

  void _handlePointerSignal(PointerSignalEvent event, bool fluidEnabled) {
    if (!fluidEnabled || !Platform.isWindows) return;
    if (event is! PointerScrollEvent) return;
    if (event.kind != PointerDeviceKind.mouse) return;
    if (!widget.controller.hasClients) return;

    final pos = widget.controller.position;
    final now = DateTime.now();
    // Reset target anchor if user stopped scrolling or dragged scrollbar manually
    if (now.difference(_lastWheelAt).inMilliseconds > 260) {
      _targetOffset = pos.pixels;
    }
    _lastWheelAt = now;

    // Slight boost for crisp, responsive desktop glide
    final delta = event.scrollDelta.dy * 1.18;
    _targetOffset = (_targetOffset + delta).clamp(pos.minScrollExtent, pos.maxScrollExtent);

    widget.controller.animateTo(
      _targetOffset,
      duration: const Duration(milliseconds: 220),
      curve: Curves.easeOutCubic,
    );
  }

  @override
  Widget build(BuildContext context) {
    final enabled = FluidMotionScope.of(context);
    if (!Platform.isWindows || !enabled) {
      return widget.child;
    }
    return Listener(
      onPointerSignal: (ev) => _handlePointerSignal(ev, enabled),
      child: widget.child,
    );
  }
}

/// GPU-composited card entrance glide (fade + subtle 14px slide) + hover lift + tap micro-scale.
/// Runs purely on transform/opacity layers so 120fps scrolling never triggers relayout.
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
    if (!fluid) {
      return widget.child;
    }

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

/// Custom ColorOS / Windows fluid page route (smooth 280ms slide + gentle scale + fade).
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
            final slide = Tween<Offset>(
              begin: const Offset(0.08, 0),
              end: Offset.zero,
            ).animate(curved);
            final scale = Tween<double>(begin: 0.98, end: 1.0).animate(curved);
            return FadeTransition(
              opacity: curved,
              child: SlideTransition(
                position: slide,
                child: ScaleTransition(
                  scale: scale,
                  child: child,
                ),
              ),
            );
          },
        );
}
