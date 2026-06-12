package com.sunyuanling.filesync.ui.components.files

import android.os.Build
import android.os.Environment
import androidx.activity.compose.BackHandler
import androidx.annotation.RequiresApi
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.sunyuanling.filesync.util.RootHelper
import com.sunyuanling.filesync.api.file.FileItem
import com.sunyuanling.filesync.util.formatFileSize
import java.io.File

/**
 * 目录选择器独立页面
 */
@OptIn(ExperimentalMaterial3Api::class)
@RequiresApi(Build.VERSION_CODES.VANILLA_ICE_CREAM)
@Composable
fun DirectoryPickerScreen(
    isRooted: Boolean,
    fileItem: FileItem,
    onDirectorySelected: (String) -> Unit,
    onDismiss: () -> Unit
) {
    // 默认都选择用户空间，Root 模式下可继续往上导航
    val initialPath = remember {
        Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS).absolutePath
    }

    var currentPath by remember { mutableStateOf(initialPath) }
    var directories by remember { mutableStateOf<List<File>>(emptyList()) }
    var loading by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val pathStack = remember { mutableStateListOf(initialPath) }

    // 返回键处理：返回上级目录或退出
    BackHandler {
        if (pathStack.size > 1) {
            pathStack.removeAt(pathStack.size - 1)
            currentPath = pathStack.last()
        } else {
            onDismiss()
        }
    }

    // 加载目录列表
    LaunchedEffect(currentPath) {
        loading = true
        error = null
        try {
            val dir = File(currentPath)
            directories = if (isRooted) {
                RootHelper.listDirectories(currentPath)
            } else {
                dir.listFiles()?.filter { it.isDirectory }?.sortedBy { it.name } ?: emptyList()
            }
        } catch (e: Exception) {
            error = "读取目录失败: ${e.message}"
            directories = emptyList()
        } finally {
            loading = false
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("选择保存位置") },
                navigationIcon = {
                    IconButton(onClick = onDismiss) {
                        Icon(
                            imageVector = Icons.Default.Check,
                            contentDescription = "关闭"
                        )
                    }
                },
                actions = {
                    TextButton(
                        onClick = { onDirectorySelected(currentPath) }
                    ) {
                        Text("选择此文件夹")
                    }
                }
            )
        }
    ) { paddingValues ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(paddingValues)
        ) {
            // 文件信息卡片
            Card(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(8.dp)
            ) {
                Column(
                    modifier = Modifier.padding(8.dp),
                    verticalArrangement = Arrangement.spacedBy(4.dp)
                ) {
                    Text(
                        text = if (fileItem.isDir) "下载文件夹" else "下载文件",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold
                    )
                    Text("文件名: ${fileItem.name}")
                    if (!fileItem.isDir) {
                        Text("大小: ${formatFileSize(fileItem.size)}")
                    }
                }
            }

            // 当前路径显示
            Surface(
                color = MaterialTheme.colorScheme.primaryContainer,
                modifier = Modifier.fillMaxWidth()
            ) {
                Column(
                    modifier = Modifier.padding(8.dp),
                    verticalArrangement = Arrangement.spacedBy(4.dp)
                ) {
                    Row(modifier = Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                        if (isRooted) {
                            Text(
                                text = "Root 模式",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.primary
                            )
                        }
                        else {
                            Text(
                                text = "普通模式",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.primary
                            )
                        }
                        Text(
                            text = "当前路径：",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onPrimaryContainer
                        )
                    }
                    Text(
                        text = currentPath,
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Medium
                    )

                }
            }

            // 返回上级按钮
            if (pathStack.size > 1) {
                TextButton(
                    onClick = {
                        pathStack.removeAt(pathStack.size - 1)
                        currentPath = pathStack.last()
                    },
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 8.dp, vertical = 4.dp)
                ) {
                    Icon(
                        imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                        contentDescription = "返回",
                        modifier = Modifier.size(14.dp)
                    )
                    Spacer(modifier = Modifier.width(4.dp))
                    Text("返回上级目录")
                }
                HorizontalDivider()
            }

            // 目录列表
            when {
                loading -> {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center
                    ) {
                        CircularProgressIndicator()
                    }
                }
                error != null -> {
                    Text(
                        text = error!!,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.padding(16.dp)
                    )
                }
                directories.isEmpty() -> {
                    Text(
                        text = "此目录下没有子文件夹",
                        modifier = Modifier.padding(16.dp),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                else -> {
                    LazyColumn(
                        modifier = Modifier.fillMaxSize()
                    ) {
                        // Root 模式下允许往上层路径导航
                        if (isRooted) {
                            val parentFile = File(currentPath).parentFile
                            if (parentFile != null) {
                                item(key = "__parent__") {
                                    ListItem(
                                        headlineContent = { Text("..", fontWeight = FontWeight.Bold) },
                                        supportingContent = { Text(parentFile.absolutePath) },
                                        leadingContent = {
                                            Icon(
                                                imageVector = Icons.AutoMirrored.Filled.ArrowBack,
                                                contentDescription = "上级目录"
                                            )
                                        },
                                        modifier = Modifier.clickable {
                                            pathStack.add(parentFile.absolutePath)
                                            currentPath = parentFile.absolutePath
                                        }
                                    )
                                    HorizontalDivider()
                                }
                            }
                        }

                        items(directories, key = { it.absolutePath }) { dir ->
                            ListItem(
                                headlineContent = { Text(dir.name) },
                                leadingContent = {
                                    Icon(
                                        imageVector = Icons.Default.Folder,
                                        contentDescription = "文件夹"
                                    )
                                },
                                modifier = Modifier.clickable {
                                    pathStack.add(dir.absolutePath)
                                    currentPath = dir.absolutePath
                                }
                            )
                            HorizontalDivider()
                        }
                    }
                }
            }
        }
    }
}