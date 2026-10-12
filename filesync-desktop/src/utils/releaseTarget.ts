export type ReleasePlatform = 'android' | 'windows' | 'linux'

/** 与桌面服务端稳定版本算法保持一致，避免不同 SemVer 被映射成同一个整数。 */
export function desktopVersionCode(version: string): number | null {
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.exec(version.trim())
  if (!match) return null
  const [major, minor, patch] = match.slice(1).map(Number)
  const code = major * 10000 + minor * 100 + patch
  return minor <= 99 && patch <= 99 && Number.isSafeInteger(code) ? code : null
}

/** Tauri 默认文件名包含版本和架构；没有识别到时保留手动输入。 */
export function inferDesktopArtifact(name: string) {
  const version = /(?:^|[_-])(\d+\.\d+\.\d+)(?=[_-]|\.)/.exec(name)?.[1]
  const lower = name.toLowerCase()
  const arch = /(?:aarch64|arm64)/.test(lower) ? 'aarch64'
    : /(?:amd64|x86_64|x64)/.test(lower) ? 'x86_64'
    : /(?:i686|ia32|(?:^|[_-])x86(?:[_-]|\.))/.test(lower) ? 'i686' : undefined
  const bundle = lower.endsWith('.exe') ? 'nsis' : lower.endsWith('.deb') ? 'deb' : lower.endsWith('.appimage') ? 'appimage' : undefined
  return {version, arch, bundle}
}
