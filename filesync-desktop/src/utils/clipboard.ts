import {invoke, isTauri} from "@tauri-apps/api/core"

/** 写剪贴板：Tauri 走 Rust 命令（带重试，剪贴板偶发被占用），Web 走浏览器 API（带 execCommand 兜底）。 */
export const copyText = async (text: string) => {
  if (isTauri()) {
    let lastErr: unknown = null
    for (let i = 0; i < 3; i++) {
      try {
        await invoke("apply_clipboard_text", { text })
        return
      } catch (e) {
        lastErr = e
        await new Promise((r) => setTimeout(r, 150))
      }
    }
    throw lastErr
  }
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const ta = document.createElement("textarea")
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    document.execCommand("copy")
    document.body.removeChild(ta)
  }
}
