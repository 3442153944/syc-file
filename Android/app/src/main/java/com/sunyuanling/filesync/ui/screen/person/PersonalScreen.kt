package com.sunyuanling.filesync.ui.screen.person

import android.os.Build
import androidx.annotation.RequiresApi
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.navigation.NavController
import com.sunyuanling.filesync.network.Request
import com.sunyuanling.filesync.router.LoginDestination
import com.sunyuanling.filesync.router.navigateAndClearBackStack
import com.sunyuanling.filesync.ui.viewModel.user.PPersonalState
import com.sunyuanling.filesync.ui.viewModel.user.PVerifyResponse
import kotlinx.coroutines.launch

@RequiresApi(Build.VERSION_CODES.O)
@Composable
fun PersonalScreen(modifier: Modifier = Modifier, navController: NavController) {
    var uiState by remember { mutableStateOf<PPersonalState>(PPersonalState.Loading) }
    var isEditing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    LaunchedEffect(Unit) {
        uiState = checkLoginStatus()
    }

    when (val state = uiState) {
        is PPersonalState.Loading -> LoadingScreen()

        is PPersonalState.NotLoggedIn -> {
            LoginScreen(
                navController = navController,
                onLoginSuccess = { user ->
//                    uiState = PPersonalState.LoggedIn(user)
                }
            )
        }

        is PPersonalState.LoggedIn -> {
            if (isEditing) {
                EditProfileScreen(
                    user = state.user,
                    onBackClick = { isEditing = false },
                    onSaveSuccess = {
                        isEditing = false
                        scope.launch {
                            uiState = PPersonalState.Loading
                            uiState = checkLoginStatus()
                        }
                    }
                )
            } else {
                UserInfoScreen(
                    user = state.user,
                    onLogout = {
                        scope.launch {
                            Request.clearToken()
                            uiState = PPersonalState.NotLoggedIn
                            navController.navigateAndClearBackStack(LoginDestination)
                        }
                    },
                    onEditClick = { isEditing = true }
                )
            }
        }

        is PPersonalState.Error -> {
            ErrorScreen(
                message = state.message,
                onRetry = {
                    scope.launch {
                        uiState = PPersonalState.Loading
                        uiState = checkLoginStatus()
                    }
                }
            )
        }
    }
}

private suspend fun checkLoginStatus(): PPersonalState {
    Request.getToken() ?: return PPersonalState.NotLoggedIn

    val result = Request.postSuspend<PVerifyResponse>("/user/verify")

    return result.fold(
        onSuccess = { response ->
            if (response.code == 200) {
                PPersonalState.LoggedIn(response.data)
            } else {
                PPersonalState.NotLoggedIn
            }
        },
        onFailure = { error ->
            PPersonalState.Error(error.message ?: "验证失败")
        }
    )
}