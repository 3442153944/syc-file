// credentialStore.ts —— Web 模式下"记住密码"的本地加密存储。
//
// Tauri 桌面端走 OS 原生凭据管理器（Windows Credential Manager / macOS Keychain /
// Linux Secret Service，见 src-tauri 的 save_remembered_credential 等命令），那是真正
// 站得住脚的方案。Web 模式没有原生层可用，只能退而求其次：用 Web Crypto 的 AES-GCM
// 加密后存 localStorage——但密钥也只能一起存在 localStorage 里，对一个能读 localStorage
// 的攻击者来说密钥和密文都拿得到，这本质上只是"不是一眼就能看见明文密码"的防君子级别
// 保护，不是真正的机密性保证（和这个 App 现有的 token 明文存 localStorage 是同一个安全
// 假设：能读你本机 localStorage 的人，反正也已经能拿到有效登录态了）。

const KEY_ENC_KEY = 'filesync_cred_key'
const KEY_ENC_PWD = 'filesync_cred_pwd'

async function getOrCreateKey(): Promise<CryptoKey> {
  const saved = localStorage.getItem(KEY_ENC_KEY)
  if (saved) {
    try {
      const jwk = JSON.parse(saved)
      return await crypto.subtle.importKey('jwk', jwk, {name: 'AES-GCM'}, true, ['encrypt', 'decrypt'])
    } catch {
      // 密钥损坏，走下面重新生成一把
    }
  }
  const key = await crypto.subtle.generateKey({name: 'AES-GCM', length: 256}, true, ['encrypt', 'decrypt'])
  const jwk = await crypto.subtle.exportKey('jwk', key)
  localStorage.setItem(KEY_ENC_KEY, JSON.stringify(jwk))
  return key
}

function bufToBase64(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf)
  let binary = ''
  for (const b of bytes) binary += String.fromCharCode(b)
  return btoa(binary)
}

function base64ToBuf(b64: string): Uint8Array {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}

export async function saveWebPassword(password: string): Promise<void> {
  const key = await getOrCreateKey()
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const cipher = await crypto.subtle.encrypt({name: 'AES-GCM', iv}, key, new TextEncoder().encode(password))
  const combined = new Uint8Array(iv.length + cipher.byteLength)
  combined.set(iv, 0)
  combined.set(new Uint8Array(cipher), iv.length)
  localStorage.setItem(KEY_ENC_PWD, bufToBase64(combined))
}

export async function loadWebPassword(): Promise<string | null> {
  const saved = localStorage.getItem(KEY_ENC_PWD)
  if (!saved) return null
  try {
    const key = await getOrCreateKey()
    const combined = base64ToBuf(saved)
    const iv = combined.slice(0, 12)
    const cipher = combined.slice(12)
    const plain = await crypto.subtle.decrypt({name: 'AES-GCM', iv}, key, cipher)
    return new TextDecoder().decode(plain)
  } catch {
    return null
  }
}

export function clearWebPassword(): void {
  localStorage.removeItem(KEY_ENC_PWD)
  localStorage.removeItem(KEY_ENC_KEY)
}
