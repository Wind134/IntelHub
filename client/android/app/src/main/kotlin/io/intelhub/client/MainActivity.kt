package io.intelhub.client

import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import android.view.Display
import androidx.core.content.ContextCompat
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class MainActivity : FlutterActivity() {
    private val channelName = "io.intelhub.client/native"
    private val secureChannelName = "io.intelhub.client/secure"
    private val keyAlias = "informationhub_local_vault_key_v1"
    private var currentRefreshRate: Float = 60.0f

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        applyHighRefreshRate(true)
    }

    private fun getOrCreateKeystoreKey(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val existing = ks.getEntry(keyAlias, null) as? KeyStore.SecretKeyEntry
        if (existing != null) return existing.secretKey

        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        val spec = KeyGenParameterSpec.Builder(
            keyAlias,
            KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT
        )
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .build()
        gen.init(spec)
        return gen.generateKey()
    }

    private fun keystoreEncrypt(plain: String): String {
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, getOrCreateKeystoreKey())
        val iv = cipher.iv
        val enc = cipher.doFinal(plain.toByteArray(Charsets.UTF_8))
        return Base64.encodeToString(iv, Base64.NO_WRAP) + ":" + Base64.encodeToString(enc, Base64.NO_WRAP)
    }

    private fun keystoreDecrypt(packed: String): String {
        val parts = packed.split(":")
        if (parts.size != 2) throw IllegalArgumentException("invalid cipher blob")
        val iv = Base64.decode(parts[0], Base64.NO_WRAP)
        val enc = Base64.decode(parts[1], Base64.NO_WRAP)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, getOrCreateKeystoreKey(), GCMParameterSpec(128, iv))
        val plain = cipher.doFinal(enc)
        return String(plain, Charsets.UTF_8)
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

        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, secureChannelName)
            .setMethodCallHandler { call, result ->
                try {
                    when (call.method) {
                        "encrypt" -> {
                            val plain = call.argument<String>("plain") ?: ""
                            result.success(keystoreEncrypt(plain))
                        }
                        "decrypt" -> {
                            val cipher = call.argument<String>("cipher") ?: ""
                            result.success(keystoreDecrypt(cipher))
                        }
                        else -> result.notImplemented()
                    }
                } catch (e: Exception) {
                    result.error("KEYSTORE_ERROR", e.message, null)
                }
            }

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
