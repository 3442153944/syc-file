// build.gradle.kts (app)
plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.android)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}


// 版本名是唯一需要手改的地方；versionCode 由它推出：主*10000 + 次*100 + 修订（1.3.1 → 10301）。
// 应用更新靠 versionCode 比大小：桌面端发布页按同一规则从版本名算出版本号，两边必须一致，
// 否则装上新包后读到的 versionCode 仍是旧值，会一直提示有更新，系统也会因版本号相同/更低拒绝覆盖安装。
val appVersionName = "1.3.3"
val appVersionCode = appVersionName.split(".").map { it.toInt() }.let { (major, minor, patch) ->
    major * 10000 + minor * 100 + patch
}

android {
    namespace = "com.sunyuanling.filesync"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.sunyuanling.filesync"
        minSdk = 26 // POI 5.x / log4j-api 用 invoke-polymorphic（MethodHandle），Android dex 仅在 API≥26 支持
        targetSdk = 37
        versionCode = appVersionCode
        versionName = appVersionName

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
        // POI 用到 java.time 等 API，minSdk 24 需核心库脱糖
        isCoreLibraryDesugaringEnabled = true
    }
    buildFeatures {
        compose = true
    }
    kotlin {
        compilerOptions {
            jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_11)
        }
    }
    // POI / xmlbeans / commons-* 携带重复的 license/notice/version 元数据，打包时需去重。
    // 注意：不要排除 .xsb（xmlbeans 编译后的 schema，运行期加载）与 META-INF/services（ServiceLoader 工厂），否则 OOXML 解析会崩。
    packaging {
        resources {
            excludes += setOf(
                "META-INF/DEPENDENCIES",
                "META-INF/LICENSE",
                "META-INF/LICENSE.txt",
                "META-INF/license.txt",
                "META-INF/NOTICE",
                "META-INF/NOTICE.txt",
                "META-INF/notice.txt",
                "META-INF/*.kotlin_module",
                "META-INF/versions/**"
            )
        }
    }
}

dependencies {
    implementation(libs.androidx.compose.foundation)
    implementation(libs.androidx.compose.runtime)
    // Core Android
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.lifecycle.runtime.ktx)
    implementation(libs.androidx.activity.compose)

    // Compose
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.ui.graphics)
    implementation(libs.androidx.compose.ui.tooling.preview)
    implementation(libs.androidx.compose.material3)
    implementation(libs.androidx.compose.material3.adaptive.navigation.suite)
    implementation(libs.androidx.compose.runtime.saveable)
    implementation(libs.androidx.compose.material.icons.extended)

    // Lifecycle & ViewModel
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.ktx)
    implementation(libs.androidx.material3)
    implementation(libs.androidx.navigation.runtime.ktx)

    // Coroutines
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.coroutines.core)

    // DataStore
    implementation(libs.androidx.datastore.preferences)

    // Network
    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)

    // BLAKE3（分片上传：叶子/Merkle树根/整文件哈希，与服务端 file_lib 一致）
    implementation(libs.blake3)

    // Testing
    testImplementation(libs.junit)
    androidTestImplementation(libs.androidx.junit)
    androidTestImplementation(libs.androidx.espresso.core)
    androidTestImplementation(platform(libs.androidx.compose.bom))
    androidTestImplementation(libs.androidx.compose.ui.test.junit4)

    // Debug
    debugImplementation(libs.androidx.compose.ui.tooling)
    debugImplementation(libs.androidx.compose.ui.test.manifest)

    //路由
    implementation(libs.androidx.navigation.compose)
    // 下载库
    implementation(libs.prdownloader)
    implementation(libs.okio)
    implementation(libs.coil)
    implementation(kotlin("reflect"))

    // 预览：Media3（视频/音频流式播放）
    implementation(libs.androidx.media3.exoplayer)
    implementation(libs.androidx.media3.ui)

    // 预览：Apache POI（Office 文档应用内解析）
    implementation(libs.poi.ooxml)
    implementation(libs.poi.scratchpad)

    // 核心库脱糖（POI 的 java.time 等）
    coreLibraryDesugaring(libs.desugar.jdk.libs)
}