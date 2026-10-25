package com.sunyuanling.filesync.mesh

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.material3.MaterialTheme
import androidx.navigation.compose.rememberNavController

/** Isolated test variant only; displays the real product screen for device acceptance. */
class MeshStatusTestActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { MaterialTheme { MeshScreen(rememberNavController()) } }
    }
}
