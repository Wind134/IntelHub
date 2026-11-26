package io.intelhub.client

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.ContextCompat
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private val channelName = "io.intelhub.client/native"

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, channelName)
            .setMethodCallHandler { call, result ->
                when (call.method) {
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
                    else -> result.notImplemented()
                }
            }
    }
}
