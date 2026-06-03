package com.example.filesync.ui.components.serverSetting

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
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavController
import com.example.filesync.AppConfig
import com.example.filesync.LogLevel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LogSettingsScreen(navController: NavController) {
    var logLevel by remember { mutableStateOf(AppConfig.loggerLevel) }
    var maxFileSizeMb by remember { mutableIntStateOf((AppConfig.logMaxFileSizeBytes / (1024 * 1024)).toInt()) }
    var maxFileCount by remember { mutableIntStateOf(AppConfig.logMaxFileCount) }
    var logDir by remember { mutableStateOf(AppConfig.logDir) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("日志设置") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    TextButton(onClick = {
                        AppConfig.loggerLevel = logLevel
                        AppConfig.logMaxFileSizeBytes = maxFileSizeMb * 1024 * 1024L
                        AppConfig.logMaxFileCount = maxFileCount
                        AppConfig.logDir = logDir.trim()
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
            // 日志级别选择
            Card(modifier = Modifier.fillMaxWidth()) {
                Column(modifier = Modifier.padding(16.dp)) {
                    Text("日志级别", fontSize = 15.sp)
                    Text("级别越低输出越详细", fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(bottom = 10.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        LogLevel.entries.forEach { level ->
                            FilterChip(
                                selected = logLevel == level,
                                onClick = { logLevel = level },
                                label = { Text(level.name, fontSize = 13.sp) }
                            )
                        }
                    }
                }
            }
            SettingsSliderItem(
                label = "单文件最大大小",
                subtitle = "超出后滚动到新文件",
                value = maxFileSizeMb,
                range = 1..50,
                unit = " MB",
                onValueChange = { maxFileSizeMb = it }
            )
            SettingsSliderItem(
                label = "最多保留文件数",
                subtitle = "超出后删除最旧的文件",
                value = maxFileCount,
                range = 1..20,
                onValueChange = { maxFileCount = it }
            )
            OutlinedTextField(
                value = logDir,
                onValueChange = { logDir = it },
                label = { Text("日志保存目录") },
                modifier = Modifier.fillMaxWidth(),
                singleLine = true
            )
        }
    }
}