import 'dart:io';

import 'package:flutter/material.dart';

/// ColorOS 17 (Aquamorphic) + Modern Windows 11 unified Light & Dark design system.
/// Windows pairs Segoe UI (Latin/digits) with Microsoft YaHei UI fallback (CJK) to avoid jagged CJK weights.
class AppTheme {
  static String? get _fontFamily => Platform.isWindows ? 'Segoe UI' : null;
  static List<String>? get _fontFallback =>
      Platform.isWindows ? const ['Microsoft YaHei UI', 'Microsoft YaHei', 'Segoe UI Emoji'] : null;

  static const PageTransitionsTheme _pageTransitions = PageTransitionsTheme(
    builders: {
      TargetPlatform.android: PredictiveBackPageTransitionsBuilder(),
      TargetPlatform.windows: FadeUpwardsPageTransitionsBuilder(),
    },
  );

  static TextTheme _buildTextTheme(Color primary, Color secondary) {
    final base = TextStyle(
      fontFamily: _fontFamily,
      fontFamilyFallback: _fontFallback,
      letterSpacing: 0,
      color: primary,
    );
    // Microsoft YaHei UI lacks a native w500 face; use w400 on Windows to keep CJK strokes crisp and uniform.
    final labelWeight = Platform.isWindows ? FontWeight.w400 : FontWeight.w500;
    return TextTheme(
      headlineSmall: base.copyWith(fontSize: 20, fontWeight: FontWeight.w600, height: 1.35),
      titleLarge: base.copyWith(fontSize: 18, fontWeight: FontWeight.w600, height: 1.35),
      titleMedium: base.copyWith(fontSize: 15, fontWeight: FontWeight.w600, height: 1.4),
      titleSmall: base.copyWith(fontSize: 13.5, fontWeight: FontWeight.w600, height: 1.4),
      bodyLarge: base.copyWith(fontSize: 14.5, fontWeight: FontWeight.w400, height: 1.6),
      bodyMedium: base.copyWith(fontSize: 13.5, fontWeight: FontWeight.w400, height: 1.55),
      bodySmall: base.copyWith(fontSize: 12, fontWeight: FontWeight.w400, height: 1.45, color: secondary),
      labelLarge: base.copyWith(fontSize: 13, fontWeight: labelWeight),
      labelMedium: base.copyWith(fontSize: 12, fontWeight: labelWeight, color: secondary),
      labelSmall: base.copyWith(fontSize: 11, fontWeight: labelWeight, color: secondary),
    );
  }

  static ThemeData light() {
    const canvas = Color(0xFFF3F6FB);
    const surface = Color(0xFFFFFFFF);
    const surfaceAlt = Color(0xFFEBF0F7);
    const border = Color(0xFFE1E8F2);
    const textPrimary = Color(0xFF0F172A);
    const textSecondary = Color(0xFF526077);
    const accent = Color(0xFF0066FF);

    return ThemeData(
      useMaterial3: true,
      brightness: Brightness.light,
      fontFamily: _fontFamily,
      fontFamilyFallback: _fontFallback,
      pageTransitionsTheme: _pageTransitions,
      scaffoldBackgroundColor: canvas,
      textTheme: _buildTextTheme(textPrimary, textSecondary),
      primaryTextTheme: _buildTextTheme(textPrimary, textSecondary),
      colorScheme: const ColorScheme.light(
        primary: accent,
        onPrimary: Colors.white,
        secondary: Color(0xFF059669),
        surface: surface,
        onSurface: textPrimary,
        surfaceContainerLowest: canvas,
        surfaceContainerLow: Color(0xFFF8FAFD),
        surfaceContainer: surfaceAlt,
        outline: border,
        outlineVariant: Color(0xFFD6DFEC),
        error: Color(0xFFE11D48),
      ),
      dividerColor: border,
      appBarTheme: const AppBarTheme(
        backgroundColor: canvas,
        foregroundColor: textPrimary,
        elevation: 0,
        scrolledUnderElevation: 0,
      ),
      cardTheme: CardThemeData(
        color: surface,
        elevation: 0,
        margin: EdgeInsets.zero,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(20),
          side: const BorderSide(color: border, width: 1),
        ),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: surface,
        contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(14),
          borderSide: const BorderSide(color: border),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(14),
          borderSide: const BorderSide(color: border),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(14),
          borderSide: const BorderSide(color: accent, width: 1.5),
        ),
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: surface,
        surfaceTintColor: Colors.transparent,
        indicatorColor: accent.withValues(alpha: 0.12),
        elevation: 0,
        height: 68,
        labelTextStyle: WidgetStateProperty.resolveWith((states) {
          final sel = states.contains(WidgetState.selected);
          return TextStyle(
            fontFamily: _fontFamily,
            fontFamilyFallback: _fontFallback,
            fontSize: 11.5,
            fontWeight: sel ? FontWeight.w600 : (Platform.isWindows ? FontWeight.w400 : FontWeight.w500),
            color: sel ? accent : textSecondary,
          );
        }),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          backgroundColor: accent,
          foregroundColor: Colors.white,
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 11),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          foregroundColor: textPrimary,
          side: const BorderSide(color: border),
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        ),
      ),
    );
  }

  static ThemeData dark() {
    const canvas = Color(0xFF0A0D14);
    const surface = Color(0xFF131926);
    const surfaceAlt = Color(0xFF1B2436);
    const border = Color(0xFF232E44);
    const textPrimary = Color(0xFFF1F5F9);
    const textSecondary = Color(0xFF94A3B8);
    const accent = Color(0xFF3B82F6);

    return ThemeData(
      useMaterial3: true,
      brightness: Brightness.dark,
      fontFamily: _fontFamily,
      fontFamilyFallback: _fontFallback,
      pageTransitionsTheme: _pageTransitions,
      scaffoldBackgroundColor: canvas,
      textTheme: _buildTextTheme(textPrimary, textSecondary),
      primaryTextTheme: _buildTextTheme(textPrimary, textSecondary),
      colorScheme: const ColorScheme.dark(
        primary: accent,
        onPrimary: Colors.white,
        secondary: Color(0xFF10B981),
        surface: surface,
        onSurface: textPrimary,
        surfaceContainerLowest: canvas,
        surfaceContainerLow: Color(0xFF0F1420),
        surfaceContainer: surfaceAlt,
        outline: border,
        outlineVariant: Color(0xFF2D3A54),
        error: Color(0xFFF43F5E),
      ),
      dividerColor: border,
      appBarTheme: const AppBarTheme(
        backgroundColor: canvas,
        foregroundColor: textPrimary,
        elevation: 0,
        scrolledUnderElevation: 0,
      ),
      cardTheme: CardThemeData(
        color: surface,
        elevation: 0,
        margin: EdgeInsets.zero,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(20),
          side: const BorderSide(color: border, width: 1),
        ),
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: surface,
        contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(14),
          borderSide: const BorderSide(color: border),
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(14),
          borderSide: const BorderSide(color: border),
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(14),
          borderSide: const BorderSide(color: accent, width: 1.5),
        ),
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: const Color(0xFF0E131F),
        surfaceTintColor: Colors.transparent,
        indicatorColor: accent.withValues(alpha: 0.18),
        elevation: 0,
        height: 68,
        labelTextStyle: WidgetStateProperty.resolveWith((states) {
          final sel = states.contains(WidgetState.selected);
          return TextStyle(
            fontFamily: _fontFamily,
            fontFamilyFallback: _fontFallback,
            fontSize: 11.5,
            fontWeight: sel ? FontWeight.w600 : (Platform.isWindows ? FontWeight.w400 : FontWeight.w500),
            color: sel ? const Color(0xFF60A5FA) : textSecondary,
          );
        }),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          backgroundColor: accent,
          foregroundColor: Colors.white,
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 11),
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: OutlinedButton.styleFrom(
          foregroundColor: textPrimary,
          side: const BorderSide(color: border),
          shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        ),
      ),
    );
  }
}
