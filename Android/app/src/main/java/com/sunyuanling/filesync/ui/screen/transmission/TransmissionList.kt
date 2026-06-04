package com.sunyuanling.filesync.ui.screen.transmission

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable
fun DownloadScreen() {
    Column(
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ){
        Text(
            text="传输列表",
            fontSize = 18.sp,
            fontWeight = FontWeight.SemiBold
        )
    }
}