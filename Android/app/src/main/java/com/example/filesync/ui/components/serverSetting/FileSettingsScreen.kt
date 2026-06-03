package com.example.filesync.ui.components.serverSetting

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.navigation.NavController
import com.example.filesync.AppConfig

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileSettingsScreen(navController: NavController) {
    var isUserRoot by remember { mutableStateOf(AppConfig.isUserRoot) }
    var pageSize by remember { mutableIntStateOf(AppConfig.filePageSize) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("文件浏览") },
                navigationIcon = {
                    IconButton(onClick = { navController.navigateUp() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    TextButton(onClick = {
                        AppConfig.isUserRoot = isUserRoot
                        AppConfig.filePageSize = pageSize
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
            Card(modifier = Modifier.fillMaxWidth()) {
                SettingsSwitchItem(
                    label = "默认用户空间",
                    subtitle = "root 模式下从 /sdcard 开始而非 /",
                    checked = isUserRoot,
                    onCheckedChange = { isUserRoot = it }
                )
            }
            SettingsSliderItem(
                label = "每页加载数量",
                subtitle = "文件列表每次加载的条目数",
                value = pageSize,
                range = 20..200,
                onValueChange = { pageSize = it }
            )
        }
    }
}