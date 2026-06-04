package com.sunyuanling.filesync.ui.components.serverSetting

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Slider
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.sunyuanling.filesync.AppConfig

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TransferSettingsScreen(navController: NavController) {
    var maxUploads by remember { mutableIntStateOf(AppConfig.maxConcurrentUploads) }
    var maxDownloads by remember { mutableIntStateOf(AppConfig.maxConcurrentDownloads) }
    var chunkSizeMb by remember { mutableIntStateOf(AppConfig.uploadChunkSize / (1024 * 1024)) }
    var downloadDir by remember { mutableStateOf(AppConfig.downloadDir) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("传输设置") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    TextButton(onClick = {
                        AppConfig.maxConcurrentUploads = maxUploads
                        AppConfig.maxConcurrentDownloads = maxDownloads
                        AppConfig.uploadChunkSize = chunkSizeMb * 1024 * 1024
                        AppConfig.downloadDir = downloadDir.trim()
                    }) { Text("保存") }
                }
            )
        }
    ) { innerPadding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(innerPadding)
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            // 最大并发上传
            SettingsSliderItem(
                label = "最大并发上传",
                subtitle = "同时进行的上传任务数",
                value = maxUploads,
                range = 1..10,
                onValueChange = { maxUploads = it }
            )
            // 最大并发下载
            SettingsSliderItem(
                label = "最大并发下载",
                subtitle = "同时进行的下载任务数",
                value = maxDownloads,
                range = 1..10,
                onValueChange = { maxDownloads = it }
            )
            // 分片大小
            SettingsSliderItem(
                label = "上传分片大小",
                subtitle = "单次上传的数据块大小",
                value = chunkSizeMb,
                range = 1..32,
                unit = "MB",
                onValueChange = { chunkSizeMb = it }
            )
            // 下载目录
            OutlinedTextField(
                value = downloadDir,
                onValueChange = { downloadDir = it },
                label = { Text("下载保存目录") },
                placeholder = { Text("FileSync/downloads") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
        }
    }
}

@Composable
fun SettingsSliderItem(
    label: String,
    subtitle: String,
    value: Int,
    range: IntRange,
    unit: String = "",
    onValueChange: (Int) -> Unit
) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.padding(16.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Column {
                    Text(label, fontSize = 15.sp)
                    Text(subtitle, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Text(
                    "$value$unit",
                    fontSize = 15.sp,
                    fontWeight = FontWeight.Medium,
                    color = MaterialTheme.colorScheme.primary
                )
            }
            Slider(
                value = value.toFloat(),
                onValueChange = { onValueChange(it.toInt()) },
                valueRange = range.first.toFloat()..range.last.toFloat(),
                steps = range.last - range.first - 1,
                modifier = Modifier.padding(top = 4.dp)
            )
        }
    }
}