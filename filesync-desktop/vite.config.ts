import {defineConfig, loadEnv} from "vite";
import vue from "@vitejs/plugin-vue";
import {resolve} from "path";
import VueDevTools from 'vite-plugin-vue-devtools'
import VueSetupExtend from 'vite-plugin-vue-setup-extend'
import checker from 'vite-plugin-checker'
// @ts-expect-error process is a nodejs global
const host = process.env.TAURI_DEV_HOST;

// https://vite.dev/config/
export default defineConfig(async ({mode}) => {
    // 加载环境变量
    const env = loadEnv(mode, process.cwd(), '');

    return {
        plugins: [
            vue(),
            checker({ vueTsc: true }),
            // 仅在开发环境启用 DevTools
            // ...(env.VITE_DEVTOOLS === 'true' ? [
            //     VueDevTools({
            //         resolveComponentPath: true,
            //         // WebStorm 的正确配置
            //         launchEditor: 'webstorm',
            //         // 可选：指定 WebStorm 可执行文件路径（如果不在 PATH 中）
            //         // editorPath: 'C:/Program Files/JetBrains/WebStorm/bin/webstorm64.exe'
            //     })
            // ] : []),
            VueDevTools({
                launchEditor: 'webstorm' // 设置默认编辑器
            }),
            VueSetupExtend()
        ],

        resolve: {
            alias: {
                '@': resolve(__dirname, './src'),
            },
        },

        // Vite options tailored for Tauri development
        clearScreen: false,
        server: {
            port: 1420,
            strictPort: true,
            host: host || false,
            hmr: host
                ? {
                    protocol: "ws",
                    host,
                    port: 1421,
                }
                : undefined,
            watch: {
                ignored: ["**/src-tauri/**"],
            },
        },
    }
});