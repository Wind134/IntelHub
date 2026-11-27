package io.intelhub.client

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.view.Display
import androidx.core.content.ContextCompat
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private val channelName = "io.intelhub.client/native"
    private var currentRefreshRate: Float = 60.0f

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Enable native highest display refresh rate (e.g. 120Hz on ColorOS LTPO) by default.
        applyHighRefreshRate(true)
    }

    private fun applyHighRefreshRate(enabled: Boolean): Double {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) {
            return 60.0
        }
        try {
            val activeDisplay: Display? = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                display
            } else {
                @Suppress("DEPRECATION")
                windowManager.defaultDisplay
            }
            if (activeDisplay == null) return currentRefreshRate.toDouble()

            val currentMode = activeDisplay.mode
            val lp = window.attributes
            if (!enabled) {
                lp.preferredDisplayModeId = 0
                lp.preferredRefreshRate = 0f
                window.attributes = lp
                currentRefreshRate = 60.0f
                return 60.0
            }

            // Pick the highest refresh-rate mode that preserves current physical resolution.
            val bestMode = activeDisplay.supportedModes
                .filter {
                    it.physicalWidth == currentMode.physicalWidth &&
                        it.physicalHeight == currentMode.physicalHeight
                }
                .maxByOrNull { it.refreshRate }
                ?: activeDisplay.supportedModes.maxByOrNull { it.refreshRate }

            if (bestMode != null) {
                lp.preferredDisplayModeId = bestMode.modeId
                lp.preferredRefreshRate = bestMode.refreshRate
                window.attributes = lp
                currentRefreshRate = bestMode.refreshRate
                return bestMode.refreshRate.toDouble()
            }
        } catch (_: Exception) {
        }
        return currentRefreshRate.toDouble()
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, channelName)
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "setHighRefreshRate" -> {
                        val enabled = call.argument<Boolean>("enabled") ?: true
                        val hz = applyHighRefreshRate(enabled)
                        result.success(hz)
                    }
                    "startKeepAlive" -> {
                        val text = call.argument<String>("text") ?: "正在实时监测突发情报"
                        val intent = Intent(this, IntelKeepAliveService::class.java).apply {
                            action = IntelKeepAliveService.ACTION_START
                            putExtra(IntelKeepAliveService.EXTRA_TEXT, text)
                        }
                        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                            ContextCompat.startForegroundService(this, intent)
                        } else {
                            startService(intent)
                        }
                        result.success(true)
                    }
                    "stopKeepAlive" -> {
                        val intent = Intent(this, IntelKeepAliveService::class.java)
                        stopService(intent)
                        result.success(true)
                    }
                    "openNotificationSettings" -> {
                        val intent = Intent().apply {
                            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                                action = Settings.ACTION_APP_NOTIFICATION_SETTINGS
                                putExtra(Settings.EXTRA_APP_PACKAGE, packageName)
                            } else {
                                action = Settings.ACTION_APPLICATION_DETAILS_SETTINGS
                                data = Uri.fromParts("package", packageName, null)
                            }
                            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                        }
                        startActivity(intent)
                        result.success(true)
                    }
                    "openBatterySettings" -> {
                        try {
                            val intent = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
                                data = Uri.parse("package:$packageName")
                                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                            }
                            startActivity(intent)
                        } catch (_: Exception) {
                            val fallback = Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS).apply {
                                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                            }
                            startActivity(fallback)
                        }
                        result.success(true)
                    }
                    else -> result.notImplemented()
                }
            }
    }
}
