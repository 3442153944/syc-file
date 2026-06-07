package com.sunyuanling.filesync.ui.screen.person

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Login
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil.compose.AsyncImage
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.api.user.UserInfo
import com.sunyuanling.filesync.util.formatDate

@RequiresApi(Build.VERSION_CODES.O)
@Composable
fun UserInfoScreen(
    user: UserInfo,
    onLogout: () -> Unit,
    onEditClick: () -> Unit
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        Spacer(modifier = Modifier.height(24.dp))

        // 头像
        Box(contentAlignment = Alignment.BottomEnd) {
            if (user.avatar?.isNotBlank() == true) {
                AsyncImage(
                    model = "${Request.baseStaticUrl}/${user.avatar!!}",
                    contentDescription = "头像",
                    modifier = Modifier
                        .size(100.dp)
                        .clip(CircleShape),
                    contentScale = ContentScale.Crop
                )
            } else {
                Icon(
                    imageVector = Icons.Default.AccountCircle,
                    contentDescription = null,
                    modifier = Modifier
                        .size(100.dp)
                        .clip(CircleShape),
                    tint = MaterialTheme.colorScheme.primary
                )
            }

            FilledIconButton(
                onClick = onEditClick,
                modifier = Modifier.size(32.dp),
                colors = IconButtonDefaults.filledIconButtonColors(
                    containerColor = MaterialTheme.colorScheme.primaryContainer
                )
            ) {
                Icon(
                    imageVector = Icons.Default.Edit,
                    contentDescription = "编辑资料",
                    modifier = Modifier.size(16.dp)
                )
            }
        }

        Text(
            text = user.username,
            fontSize = 28.sp,
            fontWeight = FontWeight.Bold
        )

        // 角色标签
        AssistChip(
            onClick = {},
            label = {
                Text(
                    text = if (user.role == "admin") "管理员" else "普通用户",
                    fontSize = 12.sp
                )
            },
            leadingIcon = {
                Icon(
                    imageVector = if (user.role == "admin") Icons.Default.AdminPanelSettings else Icons.Default.Person,
                    contentDescription = null,
                    modifier = Modifier.size(16.dp)
                )
            }
        )

        Spacer(modifier = Modifier.height(8.dp))

        // 基本信息
        Card(
            modifier = Modifier.fillMaxWidth(),
            elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
        ) {
            Column(
                modifier = Modifier.padding(20.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp)
            ) {
                Text(
                    text = "基本信息",
                    fontSize = 16.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.primary
                )

                InfoRow(icon = Icons.Default.Badge, label = "用户 ID", value = user.id.toString())
                HorizontalDivider()
                InfoRow(icon = Icons.Default.Person, label = "用户名", value = user.username)
                HorizontalDivider()
                InfoRow(icon = Icons.Default.Email, label = "邮箱", value = user.email?.ifBlank { "未设置" } ?: "未设置")
                HorizontalDivider()
                InfoRow(icon = Icons.Default.Phone, label = "手机号", value = user.phone?.ifBlank { "未设置" } ?: "未设置")
            }
        }

        // 账户信息
        Card(
            modifier = Modifier.fillMaxWidth(),
            elevation = CardDefaults.cardElevation(defaultElevation = 2.dp)
        ) {
            Column(
                modifier = Modifier.padding(20.dp),
                verticalArrangement = Arrangement.spacedBy(16.dp)
            ) {
                Text(
                    text = "账户信息",
                    fontSize = 16.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.primary
                )

                InfoRow(
                    icon = Icons.Default.Circle,
                    label = "账户状态",
                    value = if (user.status == 1) "正常" else "已禁用",
                    valueColor = if (user.status == 1)
                        MaterialTheme.colorScheme.primary
                    else
                        MaterialTheme.colorScheme.error
                )
                HorizontalDivider()
                InfoRow(
                    icon = Icons.AutoMirrored.Filled.Login,
                    label = "最后登录",
                    value = formatDate("yyyy-MM-dd HH:mm:ss", user.lastLogin ?: "未知")
                )
                HorizontalDivider()
                InfoRow(
                    icon = Icons.Default.CalendarMonth,
                    label = "注册时间",
                    value = formatDate("yyyy-MM-dd HH:mm:ss", user.createdAt ?: "未知")
                )
            }
        }

        Spacer(modifier = Modifier.height(8.dp))

        Button(
            onClick = onEditClick,
            modifier = Modifier
                .fillMaxWidth()
                .height(50.dp)
        ) {
            Icon(Icons.Default.Edit, contentDescription = null, modifier = Modifier.size(20.dp))
            Spacer(modifier = Modifier.width(8.dp))
            Text("编辑资料", fontSize = 16.sp)
        }

        OutlinedButton(
            onClick = onLogout,
            modifier = Modifier
                .fillMaxWidth()
                .height(50.dp),
            colors = ButtonDefaults.outlinedButtonColors(
                contentColor = MaterialTheme.colorScheme.error
            )
        ) {
            Icon(Icons.Default.ExitToApp, contentDescription = null, modifier = Modifier.size(20.dp))
            Spacer(modifier = Modifier.width(8.dp))
            Text("退出登录", fontSize = 16.sp)
        }

        Spacer(modifier = Modifier.height(16.dp))
    }
}

@Composable
private fun InfoRow(
    icon: ImageVector,
    label: String,
    value: String,
    valueColor: Color = Color.Unspecified
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.weight(1f, fill = false)
        ) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.primary,
                modifier = Modifier.size(20.dp)
            )
            Text(text = label, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Spacer(modifier = Modifier.width(16.dp))
        Text(
            text = value,
            fontWeight = FontWeight.Medium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            color = valueColor
        )
    }
}