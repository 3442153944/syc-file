package com.example.filesync.ui.screen.person

import android.content.Context
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil.compose.AsyncImage
import com.example.filesync.network.Request
import com.example.filesync.ui.viewModel.user.PUser
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MultipartBody
import okhttp3.RequestBody.Companion.toRequestBody

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun EditProfileScreen(
    user: PUser,
    onBackClick: () -> Unit,
    onSaveSuccess: () -> Unit
) {
    var username by remember { mutableStateOf(user.username) }
    var email by remember { mutableStateOf(user.email ?: "") }
    var phone by remember { mutableStateOf(user.phone ?: "") }
    var selectedAvatarUri by remember { mutableStateOf<Uri?>(null) }
    var isLoading by remember { mutableStateOf(false) }
    var errorMsg by remember { mutableStateOf("") }
    var successMsg by remember { mutableStateOf("") }

    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val focusManager = LocalFocusManager.current

    val imagePickerLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.GetContent()
    ) { uri: Uri? ->
        selectedAvatarUri = uri
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("编辑资料") },
                navigationIcon = {
                    IconButton(onClick = onBackClick) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    TextButton(
                        onClick = {
                            scope.launch {
                                isLoading = true
                                errorMsg = ""
                                successMsg = ""

                                val result = uploadUserInfo(
                                    context = context,
                                    username = username,
                                    email = email,
                                    phone = phone,
                                    avatarUri = selectedAvatarUri
                                )

                                result.onSuccess { response ->
                                    if (response.code == 200) {
                                        successMsg = "保存成功"
                                        kotlinx.coroutines.delay(800)
                                        onSaveSuccess()
                                    } else {
                                        errorMsg = response.message
                                    }
                                }.onFailure { error ->
                                    errorMsg = error.message ?: "保存失败"
                                }

                                isLoading = false
                            }
                        },
                        enabled = !isLoading
                    ) {
                        if (isLoading) {
                            CircularProgressIndicator(modifier = Modifier.size(20.dp), strokeWidth = 2.dp)
                        } else {
                            Text("保存", fontSize = 16.sp)
                        }
                    }
                }
            )
        }
    ) { paddingValues ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(paddingValues)
                .verticalScroll(rememberScrollState())
                .padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(20.dp)
        ) {
            // 头像选择
            Box(
                modifier = Modifier
                    .size(120.dp)
                    .clip(CircleShape)
                    .clickable { imagePickerLauncher.launch("image/*") },
                contentAlignment = Alignment.Center
            ) {
                when {
                    selectedAvatarUri != null -> {
                        AsyncImage(
                            model = selectedAvatarUri,
                            contentDescription = "新头像",
                            modifier = Modifier.fillMaxSize().clip(CircleShape),
                            contentScale = ContentScale.Crop
                        )
                    }
                    user.avatar?.isNotBlank() == true -> {
                        AsyncImage(
                            model = "${Request.baseStaticUrl}/${user.avatar!!}",
                            contentDescription = "当前头像",
                            modifier = Modifier.fillMaxSize().clip(CircleShape),
                            contentScale = ContentScale.Crop
                        )
                    }
                    else -> {
                        Icon(
                            imageVector = Icons.Default.AccountCircle,
                            contentDescription = null,
                            modifier = Modifier.fillMaxSize(),
                            tint = MaterialTheme.colorScheme.primary
                        )
                    }
                }

                Surface(
                    modifier = Modifier.fillMaxSize().clip(CircleShape),
                    color = MaterialTheme.colorScheme.surface.copy(alpha = 0.3f)
                ) {
                    Box(contentAlignment = Alignment.Center) {
                        Icon(
                            imageVector = Icons.Default.CameraAlt,
                            contentDescription = "更换头像",
                            modifier = Modifier.size(32.dp),
                            tint = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.7f)
                        )
                    }
                }
            }

            Text(
                text = "点击更换头像",
                fontSize = 14.sp,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )

            Spacer(modifier = Modifier.height(8.dp))

            OutlinedTextField(
                value = username,
                onValueChange = { username = it },
                label = { Text("用户名") },
                leadingIcon = { Icon(Icons.Default.Person, contentDescription = null) },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true,
                enabled = !isLoading,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Text, imeAction = ImeAction.Next),
                keyboardActions = KeyboardActions(onNext = { focusManager.moveFocus(FocusDirection.Down) })
            )

            email?.let { it1 ->
                OutlinedTextField(
                    value = it1,
                    onValueChange = { email = it },
                    label = { Text("邮箱") },
                    leadingIcon = { Icon(Icons.Default.Email, contentDescription = null) },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    enabled = !isLoading,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email, imeAction = ImeAction.Next),
                    keyboardActions = KeyboardActions(onNext = { focusManager.moveFocus(FocusDirection.Down) })
                )
            }

            phone?.let { it1 ->
                OutlinedTextField(
                    value = it1,
                    onValueChange = { phone = it },
                    label = { Text("手机号") },
                    leadingIcon = { Icon(Icons.Default.Phone, contentDescription = null) },
                    modifier = Modifier.fillMaxWidth(),
                    singleLine = true,
                    enabled = !isLoading,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Phone, imeAction = ImeAction.Done),
                    keyboardActions = KeyboardActions(onDone = { focusManager.clearFocus() })
                )
            }

            // 错误提示
            if (errorMsg.isNotEmpty()) {
                Card(
                    colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Row(
                        modifier = Modifier.padding(12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Icon(Icons.Default.Error, contentDescription = null, tint = MaterialTheme.colorScheme.error, modifier = Modifier.size(20.dp))
                        Text(text = errorMsg, color = MaterialTheme.colorScheme.error, fontSize = 14.sp)
                    }
                }
            }

            // 成功提示
            if (successMsg.isNotEmpty()) {
                Card(
                    colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.primaryContainer),
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Row(
                        modifier = Modifier.padding(12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Icon(Icons.Default.CheckCircle, contentDescription = null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.size(20.dp))
                        Text(text = successMsg, color = MaterialTheme.colorScheme.primary, fontSize = 14.sp)
                    }
                }
            }
        }
    }
}

/**
 * multipart/form-data 上传用户信息
 */
private suspend fun uploadUserInfo(
    context: android.content.Context,
    username: String?,
    email: String?,
    phone: String?,
    avatarUri: Uri?
): Result<UpdateUserResponse> = withContext(Dispatchers.IO) {
    try {
        val token = Request.getToken()

        val multipartBuilder = MultipartBody.Builder().setType(MultipartBody.FORM)

        if (!username.isNullOrBlank()) multipartBuilder.addFormDataPart("username", username)
        if (!email.isNullOrBlank()) multipartBuilder.addFormDataPart("email", email)
        if (!phone.isNullOrBlank()) multipartBuilder.addFormDataPart("phone", phone)

        if (avatarUri != null) {
            val inputStream = context.contentResolver.openInputStream(avatarUri)
            if (inputStream != null) {
                val bytes = inputStream.readBytes()
                inputStream.close()

                val mimeType = context.contentResolver.getType(avatarUri) ?: "image/png"
                val extension = when {
                    mimeType.contains("jpeg") || mimeType.contains("jpg") -> ".jpg"
                    mimeType.contains("gif") -> ".gif"
                    else -> ".png"
                }

                multipartBuilder.addFormDataPart(
                    "avatar",
                    "avatar$extension",
                    bytes.toRequestBody(mimeType.toMediaType())
                )
            }
        }

        val request = okhttp3.Request.Builder()
            .url("${Request.baseUrl}/user/update-info")
            .apply { token?.let { header("Token", it) } }
            .post(multipartBuilder.build())
            .build()

        val response = Request.client.newCall(request).execute()

        if (response.isSuccessful) {
            val body = response.body?.string()
            if (body != null) {
                Result.success(Request.json.decodeFromString<UpdateUserResponse>(body))
            } else {
                Result.failure(Exception("响应体为空"))
            }
        } else {
            Result.failure(Exception("HTTP ${response.code}: ${response.message}"))
        }
    } catch (e: Exception) {
        Result.failure(e)
    }
}