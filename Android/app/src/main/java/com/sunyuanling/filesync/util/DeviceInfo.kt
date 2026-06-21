package com.sunyuanling.filesync.util

import android.annotation.SuppressLint
import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.wifi.WifiManager
import android.os.BatteryManager
import android.os.Build
import android.os.Environment
import android.os.StatFs
import android.provider.Settings
import kotlinx.serialization.Serializable
import java.net.NetworkInterface

@Serializable
data class DeviceInfo(
    // 基本信息
    val deviceId: String,           // 唯一设备ID
    val deviceName: String,         // 设备名称
    val manufacturer: String,       // 制造商 e.g. Samsung
    val model: String,              // 型号 e.g. SM-G991B
    val brand: String,              // 品牌 e.g. samsung

    // 系统信息
    val osVersion: String,          // Android 14
    val sdkVersion: Int,            // 34
    val buildId: String,            // 构建ID

    // 硬件信息
    val totalRam: Long,             // 总内存（字节）
    val totalStorage: Long,         // 总存储（字节）
    val availableStorage: Long,     // 可用存储（字节）
    val batteryLevel: Int,          // 电量 0-100
    val isCharging: Boolean,        // 是否充电中
    val cpuAbi: String,             // CPU 架构 e.g. arm64-v8a

    // 网络信息
    val networkType: String,        // WIFI / CELLULAR / NONE
    val wifiSsid: String?,          // WiFi 名称
    val ipAddress: String?,         // IP 地址
    val macAddress: String?,        // MAC 地址（Android 10+ 受限）

    // App 信息
    val appVersion: String,         // App 版本
    val deviceType: String = "android"
)

object DeviceInfoUtil {

    @SuppressLint("HardwareIds")
    fun getDeviceId(context: Context): String {
        return Settings.Secure.getString(
            context.contentResolver,
            Settings.Secure.ANDROID_ID
        )
    }

    @SuppressLint("HardwareIds", "MissingPermission")
    fun collect(context: Context): DeviceInfo {
        return DeviceInfo(
            deviceId = getDeviceId(context),
            deviceName = getDeviceName(context),
            manufacturer = Build.MANUFACTURER,
            model = Build.MODEL,
            brand = Build.BRAND,

            osVersion = "Android ${Build.VERSION.RELEASE}",
            sdkVersion = Build.VERSION.SDK_INT,
            buildId = Build.ID,

            totalRam = getTotalRam(context),
            totalStorage = getTotalStorage(),
            availableStorage = getAvailableStorage(),
            batteryLevel = getBatteryLevel(context),
            isCharging = isCharging(context),
            cpuAbi = Build.SUPPORTED_ABIS.firstOrNull() ?: "unknown",

            networkType = getNetworkType(context),
            wifiSsid = getWifiSsid(context),
            ipAddress = getIpAddress(),
            macAddress = getMacAddress(),

            appVersion = getAppVersion(context)
        )
    }

    // 设备名称（用户自定义的蓝牙名称）
    private fun getDeviceName(context: Context): String {
        return Settings.Global.getString(context.contentResolver, "device_name")
            ?: Build.MODEL
    }

    // 总内存
    private fun getTotalRam(context: Context): Long {
        val activityManager = context.getSystemService(Context.ACTIVITY_SERVICE)
                as android.app.ActivityManager
        val memInfo = android.app.ActivityManager.MemoryInfo()
        activityManager.getMemoryInfo(memInfo)
        return memInfo.totalMem
    }

    // 总存储
    private fun getTotalStorage(): Long {
        val stat = StatFs(Environment.getDataDirectory().path)
        return stat.totalBytes
    }

    // 可用存储
    private fun getAvailableStorage(): Long {
        val stat = StatFs(Environment.getDataDirectory().path)
        return stat.availableBytes
    }

    // 电量
    private fun getBatteryLevel(context: Context): Int {
        val bm = context.getSystemService(Context.BATTERY_SERVICE) as BatteryManager
        return bm.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY)
    }

    // 是否充电
    private fun isCharging(context: Context): Boolean {
        val bm = context.getSystemService(Context.BATTERY_SERVICE) as BatteryManager
        return bm.isCharging
    }

    // 网络类型
    private fun getNetworkType(context: Context): String {
        val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val network = cm.activeNetwork ?: return "NONE"
        val caps = cm.getNetworkCapabilities(network) ?: return "NONE"
        return when {
            caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "WIFI"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "CELLULAR"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "ETHERNET"
            else -> "NONE"
        }
    }

    // WiFi SSID（Android 10+ 需要精确位置权限）
    @SuppressLint("MissingPermission")
    private fun getWifiSsid(context: Context): String? {
        return try {
            val wm = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
            val ssid = wm.connectionInfo.ssid
            if (ssid == "<unknown ssid>" || ssid == null) null
            else ssid.removeSurrounding("\"")
        } catch (e: Exception) { null }
    }

    // 本机 IP（遍历网卡找 IPv4）
    private fun getIpAddress(): String? {
        return try {
            NetworkInterface.getNetworkInterfaces()?.toList()
                ?.flatMap { it.inetAddresses.toList() }
                ?.firstOrNull { !it.isLoopbackAddress && it is java.net.Inet4Address }
                ?.hostAddress
        } catch (e: Exception) { null }
    }

    // MAC 地址（Android 10+ 系统返回随机值，拿不到真实值）
    private fun getMacAddress(): String? {
        return try {
            NetworkInterface.getNetworkInterfaces()?.toList()
                ?.firstOrNull { it.name == "wlan0" }
                ?.hardwareAddress
                ?.joinToString(":") { "%02x".format(it) }
        } catch (e: Exception) { null }
    }

    // App 版本
    private fun getAppVersion(context: Context): String {
        return try {
            context.packageManager.getPackageInfo(context.packageName, 0).versionName ?: "unknown"
        } catch (e: Exception) { "unknown" }
    }
}