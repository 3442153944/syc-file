package com.example.filesync.ui.components.serverSetting

import android.os.Environment
import com.example.filesync.AppConfig
import com.example.filesync.LogLevel
import java.io.File
import java.util.Properties

object ConfigManager {

    private val configFile: File
        get() {
            val dir = File(Environment.getExternalStorageDirectory(), "FileSync")
            if (!dir.exists()) dir.mkdirs()
            return File(dir, "config.conf")
        }

    fun save() {
        val props = Properties()
        props["server"] = AppConfig.server
        props["port"] = AppConfig.port.toString()
        props["isHttps"] = AppConfig.isHttps.toString()
        props["connectTimeoutMs"] = AppConfig.connectTimeoutMs.toString()
        props["readTimeoutMs"] = AppConfig.readTimeoutMs.toString()
        props["wsReconnectIntervalMs"] = AppConfig.wsReconnectIntervalMs.toString()
        props["wsMaxReconnectAttempts"] = AppConfig.wsMaxReconnectAttempts.toString()
        props["uploadChunkSize"] = AppConfig.uploadChunkSize.toString()
        props["maxConcurrentUploads"] = AppConfig.maxConcurrentUploads.toString()
        props["maxConcurrentDownloads"] = AppConfig.maxConcurrentDownloads.toString()
        props["downloadDir"] = AppConfig.downloadDir
        props["autoSyncEnabled"] = AppConfig.autoSyncEnabled.toString()
        props["autoSyncIntervalMs"] = AppConfig.autoSyncIntervalMs.toString()
        props["syncOnWifiOnly"] = AppConfig.syncOnWifiOnly.toString()
        props["loggerLevel"] = AppConfig.loggerLevel.name
        props["logDir"] = AppConfig.logDir
        props["logMaxFileSizeBytes"] = AppConfig.logMaxFileSizeBytes.toString()
        props["logMaxFileCount"] = AppConfig.logMaxFileCount.toString()
        props["isUserRoot"] = AppConfig.isUserRoot.toString()
        props["filePageSize"] = AppConfig.filePageSize.toString()
        configFile.outputStream().use {
            props.store(it, "FileSync config")
        }
    }

    fun load() {
        if (!configFile.exists()) return
        val props = Properties()
        configFile.inputStream().use { props.load(it) }

        AppConfig.server = props["server"] as? String ?: AppConfig.server
        AppConfig.port = props["port"]?.toString()?.toIntOrNull() ?: AppConfig.port
        AppConfig.isHttps = props["isHttps"]?.toString()?.toBooleanStrictOrNull() ?: AppConfig.isHttps
        AppConfig.connectTimeoutMs = props["connectTimeoutMs"]?.toString()?.toIntOrNull() ?: AppConfig.connectTimeoutMs
        AppConfig.readTimeoutMs = props["readTimeoutMs"]?.toString()?.toIntOrNull() ?: AppConfig.readTimeoutMs
        AppConfig.wsReconnectIntervalMs = props["wsReconnectIntervalMs"]?.toString()?.toLongOrNull() ?: AppConfig.wsReconnectIntervalMs
        AppConfig.wsMaxReconnectAttempts = props["wsMaxReconnectAttempts"]?.toString()?.toIntOrNull() ?: AppConfig.wsMaxReconnectAttempts
        AppConfig.uploadChunkSize = props["uploadChunkSize"]?.toString()?.toIntOrNull() ?: AppConfig.uploadChunkSize
        AppConfig.maxConcurrentUploads = props["maxConcurrentUploads"]?.toString()?.toIntOrNull() ?: AppConfig.maxConcurrentUploads
        AppConfig.maxConcurrentDownloads = props["maxConcurrentDownloads"]?.toString()?.toIntOrNull() ?: AppConfig.maxConcurrentDownloads
        AppConfig.downloadDir = props["downloadDir"] as? String ?: AppConfig.downloadDir
        AppConfig.autoSyncEnabled = props["autoSyncEnabled"]?.toString()?.toBooleanStrictOrNull() ?: AppConfig.autoSyncEnabled
        AppConfig.autoSyncIntervalMs = props["autoSyncIntervalMs"]?.toString()?.toLongOrNull() ?: AppConfig.autoSyncIntervalMs
        AppConfig.syncOnWifiOnly = props["syncOnWifiOnly"]?.toString()?.toBooleanStrictOrNull() ?: AppConfig.syncOnWifiOnly
        AppConfig.loggerLevel = props["loggerLevel"]?.toString()
            ?.let { runCatching { LogLevel.valueOf(it) }.getOrNull() } ?: AppConfig.loggerLevel
        AppConfig.logDir = props["logDir"] as? String ?: AppConfig.logDir
        AppConfig.logMaxFileSizeBytes = props["logMaxFileSizeBytes"]?.toString()?.toLongOrNull() ?: AppConfig.logMaxFileSizeBytes
        AppConfig.logMaxFileCount = props["logMaxFileCount"]?.toString()?.toIntOrNull() ?: AppConfig.logMaxFileCount
        AppConfig.isUserRoot = props["isUserRoot"]?.toString()?.toBooleanStrictOrNull() ?: AppConfig.isUserRoot
        AppConfig.filePageSize = props["filePageSize"]?.toString()?.toIntOrNull() ?: AppConfig.filePageSize
    }

    fun init() {
        if (!configFile.exists()) {
            save()
        } else {
            load()
        }
    }
}