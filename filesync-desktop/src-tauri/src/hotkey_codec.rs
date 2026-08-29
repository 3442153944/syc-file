// hotkey_codec.rs
// 职责：粘贴快传快捷键的编解码。前端录制按键组合（不让用户手打），编码成一个跟平台无关的
// 十六进制数字存到账号里（不是 "CommandOrControl+Shift+V" 这种文本），Linux/Windows/macOS
// 三端都从同一份数字解出实际按键。
//
// 编码格式（低 8 位按键 id + 高位修饰键位掩码，两边都在各自代码里手写死这张表，
// 顺序即 id，只能在末尾追加、不能重排/删除，否则已经存过的旧快捷键会解码成别的键）：
//   value = (modifier_bits << 8) | key_id
// modifier_bits：Ctrl=0x1 Shift=0x2 Alt=0x4 Meta/Win/Cmd=0x8（自定义的，不是
// keyboard-types 自己的位值，因为那个是没写死判别式的自动枚举，不同版本不保证稳定，
// 自己写一张小表更可控）。
// 前端对应的表在 filesync-desktop/src/utils/hotkeyCodec.ts，两边顺序必须完全一致。
use tauri_plugin_global_shortcut::{Code, Modifiers, Shortcut};

const KEY_TABLE: &[Code] = &[
    // 1-26：字母
    Code::KeyA, Code::KeyB, Code::KeyC, Code::KeyD, Code::KeyE, Code::KeyF, Code::KeyG,
    Code::KeyH, Code::KeyI, Code::KeyJ, Code::KeyK, Code::KeyL, Code::KeyM, Code::KeyN,
    Code::KeyO, Code::KeyP, Code::KeyQ, Code::KeyR, Code::KeyS, Code::KeyT, Code::KeyU,
    Code::KeyV, Code::KeyW, Code::KeyX, Code::KeyY, Code::KeyZ,
    // 27-36：数字
    Code::Digit0, Code::Digit1, Code::Digit2, Code::Digit3, Code::Digit4, Code::Digit5,
    Code::Digit6, Code::Digit7, Code::Digit8, Code::Digit9,
    // 37-48：功能键
    Code::F1, Code::F2, Code::F3, Code::F4, Code::F5, Code::F6,
    Code::F7, Code::F8, Code::F9, Code::F10, Code::F11, Code::F12,
    // 49-63：常用键
    Code::Space, Code::Enter, Code::Escape, Code::Tab, Code::Backspace, Code::Delete,
    Code::Insert, Code::Home, Code::End, Code::PageUp, Code::PageDown,
    Code::ArrowUp, Code::ArrowDown, Code::ArrowLeft, Code::ArrowRight,
];

/// 把粘贴快传设置里存的十六进制字符串（如 "0x108"）解码成一个可以直接注册的 Shortcut。
pub fn decode_hotkey(hex: &str) -> Result<Shortcut, String> {
    let trimmed = hex.trim().trim_start_matches("0x").trim_start_matches("0X");
    let raw = u32::from_str_radix(trimmed, 16).map_err(|_| "快捷键编码格式错误".to_string())?;
    let key_id = (raw & 0xFF) as usize;
    let mod_bits = raw >> 8;

    if key_id == 0 || key_id > KEY_TABLE.len() {
        return Err("未知的按键编码".to_string());
    }
    let code = KEY_TABLE[key_id - 1];

    let mut mods = Modifiers::empty();
    if mod_bits & 0x1 != 0 {
        mods |= Modifiers::CONTROL;
    }
    if mod_bits & 0x2 != 0 {
        mods |= Modifiers::SHIFT;
    }
    if mod_bits & 0x4 != 0 {
        mods |= Modifiers::ALT;
    }
    if mod_bits & 0x8 != 0 {
        mods |= Modifiers::SUPER;
    }

    Ok(Shortcut::new(Some(mods), code))
}
