// ui/screen/files/FileUploadScreen.kt
package com.sunyuanling.filesync.ui.screen.files

import android.net.Uri
import android.os.Environment
import android.provider.OpenableColumns
import android.util.Log
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavController
import com.sunyuanling.filesync.ui.viewModel.files.ActiveDiskViewModel
import com.sunyuanling.filesync.api.file.DiskInfo
import com.sunyuanling.filesync.ui.viewModel.files.FileListViewModel
import com.sunyuanling.filesync.ui.viewModel.files.FileUploadViewModel
import com.sunyuanling.filesync.ui.viewModel.files.UploadFileInfo
import com.sunyuanling.filesync.ui.viewModel.files.UploadState
import com.sunyuanling.filesync.util.formatFileSize
import kotlinx.coroutines.delay
import java.io.File

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileUploadScreen(
    onBackClick: () -> Unit,
    modifier: Modifier = Modifier,
    navController: NavController,
    viewModel: FileUploadViewModel = viewModel(),
) {
    val context = LocalContext.current
    val uploadState by viewModel.uploadState.collectAsState()
    val selectedFiles by viewModel.selectedFiles.collectAsState()
    val targetPath by viewModel.targetPath.collectAsState()
    val currentProgress by viewModel.currentUploadProgress.collectAsState()
    val hasRootAccess by viewModel.hasRootAccess.collectAsState()
    val isCheckingRoot by viewModel.isCheckingRoot.collectAsState()

    var showPathDialog by remember { mutableStateOf(false) }
    var showRootRequestDialog by remember { mutableStateOf(false) }

    val filePickerLauncher = rememberLauncherForActivityResult(
        contract = ActivityResultContracts.GetMultipleContents()
    ) { uris: List<Uri> ->
        val fileInfoList = uris.mapNotNull { uri ->
            try {
                context.contentResolver.query(uri, null, null, null, null)?.use { cursor ->
                    val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                    val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)

                    if (cursor.moveToFirst()) {
                        val contentResolver = context.contentResolver
                        val inputStream = contentResolver.openInputStream(uri)
                        val fileName = cursor.getString(nameIndex)
                        val tempFile = File(context.cacheDir, fileName)
                        inputStream?.use { input ->
                            tempFile.outputStream().use { output ->
                                input.copyTo(output)
                            }
                        }

                        UploadFileInfo(
                            uri = uri,
                            name = fileName,
                            path = tempFile.absolutePath,
                            size = cursor.getLong(sizeIndex),
                            mimeType = contentResolver.getType(uri)
                        )
                    } else null
                }
            } catch (e: Exception) {
                Log.e("FileUpload", "处理文件失败", e)
                null
            }
        }
        viewModel.addFiles(fileInfoList)
    }

    LaunchedEffect(uploadState) {
        when (uploadState) {
            is UploadState.Success -> {
                delay(2000)
                viewModel.resetState()
                viewModel.clearFiles()
            }
            is UploadState.PartialSuccess -> {
                delay(3000)
                viewModel.resetState()
            }
            else -> {}
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Text("文件上传")
                        if (isCheckingRoot) {
                            Text(
                                "正在检查 Root...",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        } else {
                            Text(
                                if (hasRootAccess) "Root 模式" else "用户模式",
                                style = MaterialTheme.typography.labelSmall,
                                color = if (hasRootAccess)
                                    MaterialTheme.colorScheme.primary
                                else
                                    MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onBackClick) {
                        Icon(Icons.Default.ArrowBack, "返回")
                    }
                },
                actions = {
                    if (!hasRootAccess && !isCheckingRoot) {
                        IconButton(onClick = { showRootRequestDialog = true }) {
                            Icon(Icons.Default.AdminPanelSettings, "请求 Root")
                        }
                    }
                    if (selectedFiles.isNotEmpty()) {
                        IconButton(onClick = { viewModel.clearFiles() }) {
                            Icon(Icons.Default.Clear, "清空")
                        }
                    }
                }
            )
        }
    ) { padding ->
        Column(
            modifier = modifier
                .fillMaxSize()
                .padding(padding)
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)
        ) {
            // Root 模式提示
            if (hasRootAccess) {
                Card(
                    modifier = Modifier.fillMaxWidth(),
                    colors = CardDefaults.cardColors(
                        containerColor = MaterialTheme.colorScheme.primaryContainer
                    )
                ) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(12.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Icon(
                            Icons.Default.Security,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary
                        )
                        Spacer(Modifier.width(8.dp))
                        Text(
                            "已启用 Root 权限，可访问系统所有目录",
                            style = MaterialTheme.typography.bodySmall
                        )
                    }
                }
            }

            // 目标路径选择
            OutlinedCard(
                modifier = Modifier.fillMaxWidth(),
                onClick = { showPathDialog = true }
            ) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(16.dp),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Icon(
                        Icons.Default.Folder,
                        contentDescription = null,
                        modifier = Modifier.size(24.dp)
                    )
                    Spacer(Modifier.width(12.dp))
                    Column(modifier = Modifier.weight(1f)) {
                        Text(
                            "上传目标",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                        Text(
                            targetPath.ifEmpty { "点击选择目标路径" },
                            style = MaterialTheme.typography.bodyLarge
                        )
                    }
                    Icon(Icons.Default.ChevronRight, contentDescription = null)
                }
            }

            // 文件列表
            Card(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f)
            ) {
                Column(modifier = Modifier.fillMaxSize()) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(16.dp),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text(
                            "已选择 ${selectedFiles.size} 个文件",
                            style = MaterialTheme.typography.titleMedium,
                            fontWeight = FontWeight.Bold
                        )
                        IconButton(
                            onClick = { filePickerLauncher.launch("*/*") }
                        ) {
                            Icon(Icons.Default.Add, "添加文件")
                        }
                    }

                    HorizontalDivider()

                    if (selectedFiles.isEmpty()) {
                        Box(
                            modifier = Modifier.fillMaxSize(),
                            contentAlignment = Alignment.Center
                        ) {
                            Column(
                                horizontalAlignment = Alignment.CenterHorizontally,
                                verticalArrangement = Arrangement.spacedBy(8.dp)
                            ) {
                                Icon(
                                    Icons.Default.UploadFile,
                                    contentDescription = null,
                                    modifier = Modifier.size(64.dp),
                                    tint = MaterialTheme.colorScheme.outline
                                )
                                Text(
                                    "点击右上角添加文件",
                                    color = MaterialTheme.colorScheme.outline
                                )
                            }
                        }
                    } else {
                        LazyColumn(
                            modifier = Modifier.fillMaxSize(),
                            contentPadding = PaddingValues(16.dp),
                            verticalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            items(selectedFiles, key = { it.uri.toString() }) { file ->
                                FileUploadItem(
                                    file = file,
                                    onRemove = { viewModel.removeFile(file) }
                                )
                            }
                        }
                    }
                }
            }

            // 上传进度
            currentProgress?.let { progress ->
                Card(
                    modifier = Modifier.fillMaxWidth(),
                    colors = CardDefaults.cardColors(
                        containerColor = MaterialTheme.colorScheme.primaryContainer
                    )
                ) {
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween
                        ) {
                            Text("上传中: ${progress.fileName}")
                            Text("${progress.currentIndex}/${progress.totalCount}")
                        }
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween
                        ) {
                            Text("进度")
                            Text("${(progress.progress * 100).toInt()}%")
                        }
                        LinearProgressIndicator(
                            progress = { progress.progress },
                            modifier = Modifier.fillMaxWidth()
                        )
                    }
                }
            }

            // 状态提示
            when (uploadState) {
                is UploadState.Success -> {
                    Card(
                        modifier = Modifier.fillMaxWidth(),
                        colors = CardDefaults.cardColors(
                            containerColor = MaterialTheme.colorScheme.tertiaryContainer
                        )
                    ) {
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(16.dp),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Icon(
                                Icons.Default.CheckCircle,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.tertiary
                            )
                            Spacer(Modifier.width(12.dp))
                            Text("上传成功 ${(uploadState as UploadState.Success).count} 个文件")
                        }
                    }
                }
                is UploadState.PartialSuccess -> {
                    val state = uploadState as UploadState.PartialSuccess
                    Card(
                        modifier = Modifier.fillMaxWidth(),
                        colors = CardDefaults.cardColors(
                            containerColor = MaterialTheme.colorScheme.errorContainer
                        )
                    ) {
                        Column(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(16.dp)
                        ) {
                            Text(
                                "部分上传失败",
                                fontWeight = FontWeight.Bold
                            )
                            Text("成功: ${state.successCount}  失败: ${state.failCount}")
                        }
                    }
                }
                is UploadState.Error -> {
                    Card(
                        modifier = Modifier.fillMaxWidth(),
                        colors = CardDefaults.cardColors(
                            containerColor = MaterialTheme.colorScheme.errorContainer
                        )
                    ) {
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(16.dp),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Icon(
                                Icons.Default.Error,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.error
                            )
                            Spacer(Modifier.width(12.dp))
                            Text((uploadState as UploadState.Error).message)
                        }
                    }
                }
                else -> {}
            }

            // 上传按钮
            Button(
                onClick = { viewModel.uploadFiles() },
                modifier = Modifier.fillMaxWidth(),
                enabled = selectedFiles.isNotEmpty() &&
                        targetPath.isNotEmpty() &&
                        uploadState !is UploadState.Uploading
            ) {
                Icon(Icons.Default.Upload, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("开始上传")
            }
        }
    }

    // Root 权限请求对话框
    if (showRootRequestDialog) {
        AlertDialog(
            onDismissRequest = { showRootRequestDialog = false },
            icon = { Icon(Icons.Default.AdminPanelSettings, null) },
            title = { Text("请求 Root 权限") },
            text = {
                Text("启用 Root 模式后，可以访问系统所有目录（包括 /data、/system 等）。\n\n需要您的设备已获取 Root 权限。")
            },
            confirmButton = {
                TextButton(
                    onClick = {
                        viewModel.requestRootAccess()
                        showRootRequestDialog = false
                    }
                ) {
                    Text("授权")
                }
            },
            dismissButton = {
                TextButton(onClick = { showRootRequestDialog = false }) {
                    Text("取消")
                }
            }
        )
    }

    // 路径选择对话框（集成文件浏览器）
    if (showPathDialog) {
        PathSelectionDialog(
            currentPath = targetPath,
            hasRootAccess = hasRootAccess,
            onDismiss = { showPathDialog = false },
            onPathSelected = { path ->
                viewModel.setTargetPath(path)
                showPathDialog = false
            }
        )
    }
}

@Composable
fun PathSelectionDialog(
    currentPath: String,
    hasRootAccess: Boolean,
    onDismiss: () -> Unit,
    onPathSelected: (String) -> Unit,
) {
    var showDiskList by remember { mutableStateOf(true) }
    var selectedDisk by remember { mutableStateOf<DiskInfo?>(null) }

    if (showDiskList) {
        DiskSelectionDialog(
            hasRootAccess = hasRootAccess,
            onDismiss = onDismiss,
            onDiskSelected = { disk ->
                selectedDisk = disk
                showDiskList = false
            },
            onManualPath = {
                // 直接进入目录浏览（用于 Root 模式或手动输入）
                selectedDisk = null
                showDiskList = false
            }
        )
    } else {
        DirectoryBrowserDialog(
            initialPath = selectedDisk?.path ?: (if (hasRootAccess) "/" else Environment.getExternalStorageDirectory().absolutePath),
            hasRootAccess = hasRootAccess,
            onDismiss = onDismiss,
            onBack = {
                // 返回磁盘选择
                showDiskList = true
            },
            onPathSelected = onPathSelected
        )
    }
}

@Composable
fun DiskSelectionDialog(
    hasRootAccess: Boolean,
    onDismiss: () -> Unit,
    onDiskSelected: (DiskInfo) -> Unit,
    onManualPath: () -> Unit
) {
    val diskViewModel: ActiveDiskViewModel = viewModel()
    val diskData by diskViewModel.diskData.collectAsState()
    val loading by diskViewModel.loading.collectAsState()
    val error by diskViewModel.error.collectAsState()

    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(Icons.Default.Storage, "选择磁盘") },
        title = {
            Column {
                Text("选择存储磁盘")
                if (hasRootAccess) {
                    Text(
                        "Root 模式",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.primary
                    )
                }
            }
        },
        text = {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(400.dp)
            ) {
                if (loading) {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center
                    ) {
                        CircularProgressIndicator()
                    }
                } else if (error != null) {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center
                    ) {
                        Column(
                            horizontalAlignment = Alignment.CenterHorizontally,
                            verticalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            Icon(
                                Icons.Default.Error,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.error,
                                modifier = Modifier.size(48.dp)
                            )
                            Text(
                                error ?: "加载失败",
                                color = MaterialTheme.colorScheme.error
                            )
                            TextButton(onClick = { diskViewModel.loadDisks() }) {
                                Text("重试")
                            }
                        }
                    }
                } else {
                    // 磁盘列表
                    LazyColumn(
                        modifier = Modifier.fillMaxSize(),
                        verticalArrangement = Arrangement.spacedBy(8.dp)
                    ) {
                        // Root 模式：显示根目录选项
                        if (hasRootAccess) {
                            item {
                                Card(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .clickable { onManualPath() },
                                    colors = CardDefaults.cardColors(
                                        containerColor = MaterialTheme.colorScheme.primaryContainer
                                    )
                                ) {
                                    Row(
                                        modifier = Modifier
                                            .fillMaxWidth()
                                            .padding(16.dp),
                                        verticalAlignment = Alignment.CenterVertically
                                    ) {
                                        Icon(
                                            Icons.Default.Folder,
                                            contentDescription = null,
                                            modifier = Modifier.size(40.dp),
                                            tint = MaterialTheme.colorScheme.primary
                                        )
                                        Spacer(Modifier.width(16.dp))
                                        Column(modifier = Modifier.weight(1f)) {
                                            Text(
                                                "根目录",
                                                style = MaterialTheme.typography.titleMedium,
                                                fontWeight = FontWeight.Bold
                                            )
                                            Text(
                                                "/",
                                                style = MaterialTheme.typography.bodySmall,
                                                color = MaterialTheme.colorScheme.onPrimaryContainer
                                            )
                                        }
                                        Icon(
                                            Icons.Default.ChevronRight,
                                            contentDescription = null
                                        )
                                    }
                                }
                            }

                            item {
                                HorizontalDivider(modifier = Modifier.padding(vertical = 8.dp))
                            }
                        }

                        // 显示可用磁盘
                        diskData?.let { data ->
                            val disksToShow = if (hasRootAccess) {
                                // Root 模式显示所有磁盘
                                data.allDisks
                            } else {
                                // 普通模式只显示允许访问的磁盘
                                data.allowedDisks ?: data.allDisks.filter { it.isAllowed }
                            }

                            if (disksToShow.isEmpty()) {
                                item {
                                    Box(
                                        modifier = Modifier
                                            .fillMaxWidth()
                                            .height(100.dp),
                                        contentAlignment = Alignment.Center
                                    ) {
                                        Text(
                                            "暂无可用磁盘",
                                            color = MaterialTheme.colorScheme.onSurfaceVariant
                                        )
                                    }
                                }
                            } else {
                                items(disksToShow, key = { it.path }) { disk ->
                                    DiskItem(
                                        disk = disk,
                                        onClick = { onDiskSelected(disk) }
                                    )
                                }
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {},
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("取消")
            }
        }
    )
}

@Composable
fun DiskItem(
    disk: DiskInfo,
    onClick: () -> Unit
) {
    Card(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(16.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            // 磁盘图标
            Icon(
                if (disk.isSsd) Icons.Default.SdCard else Icons.Default.Storage,
                contentDescription = null,
                modifier = Modifier.size(40.dp),
                tint = when {
                    !disk.isAccessible -> MaterialTheme.colorScheme.error
                    disk.usedPercent > 90 -> MaterialTheme.colorScheme.error
                    disk.usedPercent > 70 -> MaterialTheme.colorScheme.tertiary
                    else -> MaterialTheme.colorScheme.primary
                }
            )

            Spacer(Modifier.width(16.dp))

            // 磁盘信息
            Column(modifier = Modifier.weight(1f)) {
                Row(
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text(
                        disk.path,
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold
                    )

                    if (disk.isSsd) {
                        Surface(
                            color = MaterialTheme.colorScheme.primaryContainer,
                            shape = MaterialTheme.shapes.small
                        ) {
                            Text(
                                "SSD",
                                modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onPrimaryContainer
                            )
                        }
                    }
                }

                Text(
                    "${disk.freeGb} 可用 / ${disk.totalGb}",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )

                if (disk.fstype.isNotEmpty()) {
                    Text(
                        disk.fstype,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }

                // 使用进度条
                Spacer(Modifier.height(4.dp))
                LinearProgressIndicator(
                    progress = { (disk.usedPercent / 100.0).toFloat() },
                    modifier = Modifier.fillMaxWidth(),
                    color = when {
                        disk.usedPercent > 90 -> MaterialTheme.colorScheme.error
                        disk.usedPercent > 70 -> MaterialTheme.colorScheme.tertiary
                        else -> MaterialTheme.colorScheme.primary
                    }
                )
            }

            Spacer(Modifier.width(8.dp))

            Icon(
                Icons.Default.ChevronRight,
                contentDescription = null
            )
        }
    }
}

@Composable
fun DirectoryBrowserDialog(
    initialPath: String,
    hasRootAccess: Boolean,
    onDismiss: () -> Unit,
    onBack: () -> Unit,
    onPathSelected: (String) -> Unit
) {
    val fileListViewModel: FileListViewModel = viewModel()
    val fileData by fileListViewModel.fileData.collectAsState()
    val loading by fileListViewModel.loading.collectAsState()
    val error by fileListViewModel.error.collectAsState()

    LaunchedEffect(initialPath) {
        fileListViewModel.loadDirectory(initialPath)
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(Icons.Default.Folder, "浏览目录") },
        title = {
            Column {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    IconButton(onClick = onBack) {
                        Icon(Icons.Default.ArrowBack, "返回磁盘选择")
                    }
                    Column(modifier = Modifier.weight(1f)) {
                        Text("选择目标路径")
                        Text(
                            fileData?.currentPath ?: initialPath,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1
                        )
                    }
                }
            }
        },
        text = {
            Column(modifier = Modifier.fillMaxWidth()) {
                if (loading) {
                    Box(
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(300.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        CircularProgressIndicator()
                    }
                } else if (error != null) {
                    Box(
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(300.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        Column(
                            horizontalAlignment = Alignment.CenterHorizontally,
                            verticalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            Icon(
                                Icons.Default.Error,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.error
                            )
                            Text(
                                error ?: "加载失败",
                                color = MaterialTheme.colorScheme.error
                            )
                            TextButton(onClick = { fileListViewModel.refresh() }) {
                                Text("重试")
                            }
                        }
                    }
                } else {
                    LazyColumn(
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(300.dp)
                    ) {
                        // 返回上级目录
                        fileData?.parentPath?.let { parent ->
                            if (parent.isNotEmpty()) {
                                item {
                                    ListItem(
                                        headlineContent = { Text("..") },
                                        supportingContent = { Text("返回上级") },
                                        leadingContent = {
                                            Icon(Icons.Default.ArrowUpward, null)
                                        },
                                        modifier = Modifier.clickable {
                                            fileListViewModel.navigateToParent()
                                        }
                                    )
                                    HorizontalDivider()
                                }
                            }
                        }

                        // 只显示目录
                        fileData?.items?.filter { it.isDir }?.let { dirs ->
                            if (dirs.isEmpty()) {
                                item {
                                    Box(
                                        modifier = Modifier
                                            .fillMaxWidth()
                                            .height(100.dp),
                                        contentAlignment = Alignment.Center
                                    ) {
                                        Text(
                                            "当前目录为空",
                                            color = MaterialTheme.colorScheme.onSurfaceVariant
                                        )
                                    }
                                }
                            } else {
                                items(dirs, key = { it.path }) { item ->
                                    ListItem(
                                        headlineContent = { Text(item.name) },
                                        supportingContent = {
                                            Text("${item.childrenCount} 项")
                                        },
                                        leadingContent = {
                                            Icon(Icons.Default.Folder, null)
                                        },
                                        trailingContent = {
                                            Icon(Icons.Default.ChevronRight, null)
                                        },
                                        modifier = Modifier.clickable {
                                            fileListViewModel.navigateTo(item.path)
                                        }
                                    )
                                }
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                onClick = {
                    fileData?.currentPath?.let { onPathSelected(it) }
                },
                enabled = fileData?.currentPath != null
            ) {
                Text("选择当前目录")
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("取消")
            }
        }
    )
}

@Composable
fun FileUploadItem(
    file: UploadFileInfo,
    onRemove: () -> Unit
) {
    Card(
        modifier = Modifier.fillMaxWidth()
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Icon(
                Icons.Default.InsertDriveFile,
                contentDescription = null,
                modifier = Modifier.size(40.dp),
                tint = MaterialTheme.colorScheme.primary
            )
            Spacer(Modifier.width(12.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    file.name,
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.Medium
                )
                Text(
                    formatFileSize(file.size),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            IconButton(onClick = onRemove) {
                Icon(Icons.Default.Close, "移除")
            }
        }
    }
}
