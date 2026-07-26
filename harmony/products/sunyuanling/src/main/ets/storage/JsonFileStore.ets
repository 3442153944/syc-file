import fs from '@ohos.file.fs';

/**
 * JsonFileStore
 * 沙盒里的 JSON 文件读写（原子写）。
 *
 * ── 为什么不用 preferences ──────────────────────────────────
 * preferences 是给「小配置项」设计的，官方文档对 string value 的长度上限说法不一
 * （8192 字节 / 16MB 两种说法都能查到），而同步基线台账、搬运流水这类数据
 * 是**按文件数线性增长**的：几百个文件 × (相对路径 + 64 位 hash) 轻松几十上百 KB。
 * 压在一个 key 里既可能被截断，又要在每次防抖保存时重写整串，纯属浪费。
 *
 * 这类「体量随数据增长」的台账一律落沙盒普通文件：无长度限制、写入代价可控。
 * 只有真正的小配置（服务器地址、开关）才留在 preferences。
 *
 * ⚠ 一律异步 fs，避免主线程做文件 IO 导致 appfreeze。
 * ⚠ 写入走 tmp + rename：进程在写一半时被杀（后台冻结是常态），
 *   不会留下半截 JSON 把台账整个毁掉。
 */

/** 读文本；文件不存在/读失败一律返回空串（调用方按「没有台账」处理）。 */
export async function readTextFile(path: string): Promise<string> {
  try {
    if (!await fs.access(path)) return '';
    return await fs.readText(path);
  } catch (e) {
    console.error(`[JsonFile] 读取失败 ${path}: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    return '';
  }
}

/**
 * 原子写文本：先写 <path>.tmp，再 rename 覆盖目标。
 * 中途被杀最多留一个 .tmp，原文件始终完整。
 */
export async function writeTextFileAtomic(path: string, text: string): Promise<boolean> {
  const tmp: string = `${path}.tmp`;
  try {
    const f: fs.File = await fs.open(tmp, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
    try {
      await fs.write(f.fd, text);
    } finally {
      await fs.close(f.fd);
    }
    // rename 目标已存在时部分实现会失败，先删再改名
    try { await fs.unlink(path); } catch (e) { /* 原本就不存在 */ }
    await fs.rename(tmp, path);
    return true;
  } catch (e) {
    console.error(`[JsonFile] 写入失败 ${path}: ${e instanceof Error ? e.message : JSON.stringify(e)}`);
    try { await fs.unlink(tmp); } catch (e2) { /* ignore */ }
    return false;
  }
}
