// hotkeyCodec.ts
// 粘贴快传快捷键的编解码：录制按键组合 → 编码成平台无关的十六进制数字（不存 "Ctrl+Shift+V"
// 这种文本，否则 Linux 上不一定能直接拿这段文本注册）。Rust 那边的解码表在
// src-tauri/src/hotkey_codec.rs，两边顺序必须完全一致——只能在末尾追加、不能重排/删除，
// 否则已经存过的旧快捷键会解码成别的键。
//
// 编码格式：value = (modifierBits << 8) | keyId
// modifierBits：Ctrl=0x1 Shift=0x2 Alt=0x4 Meta/Win/Cmd=0x8

const KEY_TABLE: { code: string; label: string }[] = [
  // 1-26：字母
  {code: 'KeyA', label: 'A'}, {code: 'KeyB', label: 'B'}, {code: 'KeyC', label: 'C'},
  {code: 'KeyD', label: 'D'}, {code: 'KeyE', label: 'E'}, {code: 'KeyF', label: 'F'},
  {code: 'KeyG', label: 'G'}, {code: 'KeyH', label: 'H'}, {code: 'KeyI', label: 'I'},
  {code: 'KeyJ', label: 'J'}, {code: 'KeyK', label: 'K'}, {code: 'KeyL', label: 'L'},
  {code: 'KeyM', label: 'M'}, {code: 'KeyN', label: 'N'}, {code: 'KeyO', label: 'O'},
  {code: 'KeyP', label: 'P'}, {code: 'KeyQ', label: 'Q'}, {code: 'KeyR', label: 'R'},
  {code: 'KeyS', label: 'S'}, {code: 'KeyT', label: 'T'}, {code: 'KeyU', label: 'U'},
  {code: 'KeyV', label: 'V'}, {code: 'KeyW', label: 'W'}, {code: 'KeyX', label: 'X'},
  {code: 'KeyY', label: 'Y'}, {code: 'KeyZ', label: 'Z'},
  // 27-36：数字
  {code: 'Digit0', label: '0'}, {code: 'Digit1', label: '1'}, {code: 'Digit2', label: '2'},
  {code: 'Digit3', label: '3'}, {code: 'Digit4', label: '4'}, {code: 'Digit5', label: '5'},
  {code: 'Digit6', label: '6'}, {code: 'Digit7', label: '7'}, {code: 'Digit8', label: '8'},
  {code: 'Digit9', label: '9'},
  // 37-48：功能键
  {code: 'F1', label: 'F1'}, {code: 'F2', label: 'F2'}, {code: 'F3', label: 'F3'},
  {code: 'F4', label: 'F4'}, {code: 'F5', label: 'F5'}, {code: 'F6', label: 'F6'},
  {code: 'F7', label: 'F7'}, {code: 'F8', label: 'F8'}, {code: 'F9', label: 'F9'},
  {code: 'F10', label: 'F10'}, {code: 'F11', label: 'F11'}, {code: 'F12', label: 'F12'},
  // 49-63：常用键
  {code: 'Space', label: '空格'}, {code: 'Enter', label: 'Enter'}, {code: 'Escape', label: 'Esc'},
  {code: 'Tab', label: 'Tab'}, {code: 'Backspace', label: 'Backspace'}, {code: 'Delete', label: 'Delete'},
  {code: 'Insert', label: 'Insert'}, {code: 'Home', label: 'Home'}, {code: 'End', label: 'End'},
  {code: 'PageUp', label: 'PageUp'}, {code: 'PageDown', label: 'PageDown'},
  {code: 'ArrowUp', label: '↑'}, {code: 'ArrowDown', label: '↓'},
  {code: 'ArrowLeft', label: '←'}, {code: 'ArrowRight', label: '→'},
]

const CODE_TO_ID = new Map<string, number>(KEY_TABLE.map((k, i) => [k.code, i + 1]))

const MOD_CTRL = 0x1
const MOD_SHIFT = 0x2
const MOD_ALT = 0x4
const MOD_META = 0x8

/** event.code 是否属于修饰键本身（不能拿修饰键单独当主键）。 */
function isModifierCode(code: string): boolean {
  return /^(Control|Shift|Alt|Meta|OS)(Left|Right)?$/.test(code)
}

export interface CapturedHotkey {
  /** 存到账号里的编码值，比如 0x108（Ctrl+F） */
  code: number
  /** 仅用于界面展示，不落库 */
  label: string
}

/**
 * 从一次 keydown 事件里提取快捷键组合。修饰键单独按下（还没按到真正的键）返回 null，
 * 调用方应该继续等待，而不是当成录制失败。真正非修饔键但不在支持表里的（比如中文输入法
 * 相关按键）也返回 null，调用方提示"不支持这个键"。
 */
export function captureHotkey(event: KeyboardEvent): CapturedHotkey | null {
  if (isModifierCode(event.code)) return null
  const keyId = CODE_TO_ID.get(event.code)
  if (!keyId) return null

  let mods = 0
  const labelParts: string[] = []
  if (event.ctrlKey) {
    mods |= MOD_CTRL
    labelParts.push('Ctrl')
  }
  if (event.shiftKey) {
    mods |= MOD_SHIFT
    labelParts.push('Shift')
  }
  if (event.altKey) {
    mods |= MOD_ALT
    labelParts.push('Alt')
  }
  if (event.metaKey) {
    mods |= MOD_META
    labelParts.push('Meta')
  }
  // 全局快捷键至少要带一个修饰键，否则会把这个键在所有地方的正常输入都吃掉
  if (mods === 0) return null

  labelParts.push(KEY_TABLE[keyId - 1].label)
  return {code: (mods << 8) | keyId, label: labelParts.join('+')}
}

/** 编码值 → 十六进制字符串（存库/传给后端用）。 */
export function encodeHotkeyHex(code: number): string {
  return '0x' + code.toString(16)
}

/** 十六进制字符串 → 人类可读的展示文本，解析失败返回原始字符串（不崩页面）。 */
export function decodeHotkeyLabel(hex: string | null | undefined): string {
  if (!hex) return '未设置'
  const raw = parseInt(hex, 16)
  if (Number.isNaN(raw)) return hex
  const keyId = raw & 0xff
  const mods = raw >> 8
  const key = KEY_TABLE[keyId - 1]
  if (!key) return hex

  const parts: string[] = []
  if (mods & MOD_CTRL) parts.push('Ctrl')
  if (mods & MOD_SHIFT) parts.push('Shift')
  if (mods & MOD_ALT) parts.push('Alt')
  if (mods & MOD_META) parts.push('Meta')
  parts.push(key.label)
  return parts.join('+')
}
