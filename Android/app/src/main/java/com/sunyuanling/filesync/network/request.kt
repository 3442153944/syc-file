// network/request.kt
package com.sunyuanling.filesync.network

import android.content.Context
import android.util.Log
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import com.sunyuanling.filesync.AppConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.SerializationStrategy
import kotlinx.serialization.serializer
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.net.URLEncoder
import java.util.concurrent.TimeUnit

private val Context.dataStore: DataStore<Preferences> by preferencesDataStore(name = "secure_prefs")

object Request {

    var baseUrl = "${AppConfig.getBaseUrl()}/v1"
        set(value) {
            field = value.trimEnd('/')
            Log.d(TAG, "基础 URL: $field")
        }

    var baseStaticUrl = AppConfig.getBaseUrl()
        set(value) {
            field = value.trimEnd('/')
            Log.d(TAG, "基础静态 URL: $field")
        }

    private var appContext: Context? = null

    private val TOKEN_KEY = stringPreferencesKey("token")
    private val USERNAME_KEY = stringPreferencesKey("saved_username")
    private val PASSWORD_KEY = stringPreferencesKey("saved_password")
    private val REMEMBER_PASSWORD_KEY = booleanPreferencesKey("remember_password")

    // token 自动提取的接口白名单
    val TOKEN_ENDPOINTS = setOf("/user/login", "/user/verify")

    val client = OkHttpClient.Builder()
        .connectTimeout(30, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .writeTimeout(30, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build()

    val json = Json {
        ignoreUnknownKeys = true
        isLenient = true
        prettyPrint = false
    }

    fun init(context: Context) {
        appContext = context.applicationContext
        Log.d(TAG, "Request 初始化成功")
    }

    // ==================== Token 管理 ====================

    suspend fun saveToken(token: String) {
        appContext?.dataStore?.edit { preferences ->
            preferences[TOKEN_KEY] = token
        }
        Log.d(TAG, "Token 已保存")
    }

    suspend fun getToken(): String? {
        return appContext?.dataStore?.data?.map { preferences ->
            preferences[TOKEN_KEY]
        }?.first()
    }

    suspend fun clearToken() {
        appContext?.dataStore?.edit { preferences ->
            preferences.remove(TOKEN_KEY)
        }
        Log.d(TAG, "Token 已清除")
    }

    suspend fun hasToken(): Boolean {
        return !getToken().isNullOrEmpty()
    }

    // ==================== 记住密码 ====================

    suspend fun saveCredentials(username: String, password: String, remember: Boolean) {
        appContext?.dataStore?.edit { preferences ->
            preferences[REMEMBER_PASSWORD_KEY] = remember
            if (remember) {
                preferences[USERNAME_KEY] = username
                preferences[PASSWORD_KEY] = password
            } else {
                preferences.remove(USERNAME_KEY)
                preferences.remove(PASSWORD_KEY)
            }
        }
        Log.d(TAG, "凭据已保存: remember=$remember")
    }

    suspend fun getSavedCredentials(): Triple<String, String, Boolean>? {
        return appContext?.dataStore?.data?.map { preferences ->
            val remember = preferences[REMEMBER_PASSWORD_KEY] ?: false
            val username = preferences[USERNAME_KEY] ?: ""
            val password = preferences[PASSWORD_KEY] ?: ""
            Triple(username, password, remember)
        }?.first()
    }

    suspend fun clearCredentials() {
        appContext?.dataStore?.edit { preferences ->
            preferences.remove(USERNAME_KEY)
            preferences.remove(PASSWORD_KEY)
            preferences.remove(REMEMBER_PASSWORD_KEY)
        }
        Log.d(TAG, "凭据已清除")
    }

    // ==================== 回调风格请求 ====================

    /**
     * GET 请求
     */
    suspend inline fun <reified T> get(
        endpoint: String,
        queryParams: Map<String, String> = emptyMap(),
        noinline onResult: (Result<T>) -> Unit = {}
    ) {
        val result = requestSuspendNoBody<T>("GET", endpoint, queryParams)
        withContext(Dispatchers.Main) {
            onResult(result)
        }
    }

    /**
     * POST 请求（有请求体）
     */
    suspend inline fun <reified T, reified B> post(
        endpoint: String,
        body: B? = null,
        noinline onResult: (Result<T>) -> Unit = {}
    ) {
        val result = requestSuspend<T, B>("POST", endpoint, body, json.serializersModule.serializer<B>())
        withContext(Dispatchers.Main) {
            onResult(result)
        }
    }

    /**
     * POST 请求（无请求体）
     */
    suspend inline fun <reified T> post(
        endpoint: String,
        noinline onResult: (Result<T>) -> Unit = {}
    ) {
        val result = requestSuspend<T, Unit>("POST", endpoint, null, null)
        withContext(Dispatchers.Main) {
            onResult(result)
        }
    }

    // ==================== Suspend 风格请求 ====================

    /**
     * GET 请求 - suspend 版本
     */
    suspend inline fun <reified T> getSuspend(
        endpoint: String,
        queryParams: Map<String, String> = emptyMap()
    ): Result<T> {
        return requestSuspendNoBody("GET", endpoint, queryParams)
    }

    /**
     * POST 请求 - suspend 版本（有请求体）
     */
    suspend inline fun <reified T, reified B> postSuspend(
        endpoint: String,
        body: B? = null
    ): Result<T> {
        return requestSuspend("POST", endpoint, body, json.serializersModule.serializer<B>())
    }

    /**
     * POST 请求 - suspend 版本（无请求体）
     */
    suspend inline fun <reified T> postSuspend(
        endpoint: String
    ): Result<T> {
        return requestSuspend<T, Unit>("POST", endpoint, null, null)
    }

    // ==================== 核心请求方法 ====================

    /**
     * 通用请求方法，无请求体（用于 GET 等）
     */
    suspend inline fun <reified T> requestSuspendNoBody(
        method: String,
        endpoint: String,
        queryParams: Map<String, String> = emptyMap()
    ): Result<T> = withContext(Dispatchers.IO) {
        try {
            var url = "$baseUrl$endpoint"
            if (queryParams.isNotEmpty()) {
                val queryString = queryParams.entries.joinToString("&") { (k, v) ->
                    "$k=${URLEncoder.encode(v, "UTF-8")}"
                }
                url += "?$queryString"
            }
            Log.d(TAG, "$method $url")

            val token = getToken()

            val requestBuilder = Request.Builder()
                .url(url)
                .apply {
                    token?.let { header("Token", it) }
                    get()
                }

            val response = client.newCall(requestBuilder.build()).execute()

            if (!response.isSuccessful) {
                if (response.code == 401) {
                    Log.w(TAG, "认证失败，清除 token 并跳转登录")
                    clearToken()
                    AuthManager.notifyTokenExpired()
                }
                return@withContext Result.failure(
                    Exception("HTTP ${response.code}: ${response.message}")
                )
            }

            val responseBody = response.body?.string()
            if (responseBody.isNullOrEmpty()) {
                return@withContext Result.failure(Exception("响应体为空"))
            }

            Log.d(TAG, "响应: ${responseBody.take(200)}")

            val jsonObj = json.parseToJsonElement(responseBody).jsonObject
            val code = jsonObj["code"]?.jsonPrimitive?.intOrNull
            val message = jsonObj["message"]?.jsonPrimitive?.content ?: "未知错误"

            if (code != 200) {
                Log.w(TAG, "业务错误: code=$code, message=$message")
                return@withContext Result.failure(Exception(message))
            }

            val result = json.decodeFromString<T>(responseBody)
            Result.success(result)

        } catch (e: Exception) {
            Log.e(TAG, "请求失败: ${e.message}", e)
            Result.failure(e)
        }
    }

    /**
     * 通用请求方法，直接返回 Result<T>
     *
     * 逻辑：
     * 1. 发送 HTTP 请求
     * 2. HTTP 成功后，先用 JsonObject 解析原始 JSON，检查业务 code
     * 3. code == 200 时：如果是 login/verify 接口，自动提取并保存 token
     * 4. code != 200 时：返回业务错误信息（message 字段）
     * 5. HTTP 401 时：自动清除本地 token
     */
    suspend inline fun <reified T, B> requestSuspend(
        method: String,
        endpoint: String,
        body: B?,
        serializer: SerializationStrategy<B>?
    ): Result<T> = withContext(Dispatchers.IO) {
        try {
            val url = "$baseUrl$endpoint"
            Log.d(TAG, "$method $url")

            val token = getToken()

            val requestBuilder = Request.Builder()
                .url(url)
                .apply {
                    token?.let { header("Token", it) }
                }

            // 构建请求体
            when (method.uppercase()) {
                "GET" -> requestBuilder.get()
                else -> {
                    val requestBody = if (body != null && serializer != null) {
                        json.encodeToString(serializer, body)
                            .toRequestBody("application/json".toMediaType())
                    } else {
                        "{}".toRequestBody("application/json".toMediaType())
                    }
                    when (method.uppercase()) {
                        "POST" -> requestBuilder.post(requestBody)
                        "PUT" -> requestBuilder.put(requestBody)
                        "DELETE" -> requestBuilder.delete(requestBody)
                    }
                }
            }

            val response = client.newCall(requestBuilder.build()).execute()

            if (!response.isSuccessful) {
                if (response.code == 401) {
                    Log.w(TAG, "认证失败，清除 token 并跳转登录")
                    clearToken()
                    AuthManager.notifyTokenExpired()
                }
                return@withContext Result.failure(
                    Exception("HTTP ${response.code}: ${response.message}")
                )
            }

            val responseBody = response.body?.string()
            if (responseBody.isNullOrEmpty()) {
                return@withContext Result.failure(Exception("响应体为空"))
            }

            Log.d(TAG, "响应: ${responseBody.take(200)}")

            // 先用 JsonObject 解析，检查业务 code
            val jsonObj = json.parseToJsonElement(responseBody).jsonObject
            val code = jsonObj["code"]?.jsonPrimitive?.intOrNull
            val message = jsonObj["message"]?.jsonPrimitive?.content ?: "未知错误"

            if (code != 200) {
                Log.w(TAG, "业务错误: code=$code, message=$message")
                return@withContext Result.failure(Exception(message))
            }

            // 如果是 login/verify 接口，自动提取 token
            if (TOKEN_ENDPOINTS.any { endpoint.endsWith(it) }) {
                tryExtractToken(jsonObj)
            }

            // 反序列化为目标类型
            val result = json.decodeFromString<T>(responseBody)
            Result.success(result)

        } catch (e: Exception) {
            Log.e(TAG, "请求失败: ${e.message}", e)
            Result.failure(e)
        }
    }

    /**
     * 从 JSON 响应中提取 token（仅用于 login/verify 接口）
     * 安全地尝试从 data.token 中提取，不存在则跳过
     */
    suspend fun tryExtractToken(jsonObj: JsonObject) {
        try {
            val data = jsonObj["data"]?.jsonObject ?: return
            val tokenValue = data["token"]?.jsonPrimitive?.content
            if (!tokenValue.isNullOrEmpty()) {
                saveToken(tokenValue)
                Log.d(TAG, "自动提取并保存了新 token")
            }
        } catch (e: Exception) {
            // verify 接口的 data 里没有 token 字段，这是正常的
            Log.d(TAG, "未从响应中提取到 token: ${e.message}")
        }
    }

    const val TAG = "Request"
}