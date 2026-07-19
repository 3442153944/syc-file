/**
 * Blake3Util
 * 纯 ArkTS BLAKE3 实现，移植自官方参考实现（BLAKE3-team/BLAKE3 reference_impl.rs）。
 * 与服务端 file_lib（Rust blake3 crate）/ 桌面端 Rust blake3 / Android rctcwyvrn:blake3 逐字节一致。
 *
 * 提供两个核心能力：
 *  - hash(bytes): Uint8Array(32)  标准 BLAKE3 哈希（用于 leaf_hashes / merkle_root parent / file_hash）
 *  - Blake3Hasher 流式哈希器（用于大文件分块喂入）
 *
 * 上传协议使用方式（见 file_lib/README.md）：
 *  - leaf_hashes[i] = blake3(chunk_i_bytes)
 *  - merkle_root = 两两 parent = blake3(left ‖ right)，奇数进位，空 → blake3("")
 *  - file_hash = blake3(整文件字节流)
 */

const OUT_LEN: number = 32;
const BLOCK_LEN: number = 64;
const CHUNK_LEN: number = 1024;

const CHUNK_START: number = 1 << 0;
const CHUNK_END: number = 1 << 1;
const PARENT: number = 1 << 2;
const ROOT: number = 1 << 3;

const IV: Uint32Array = new Uint32Array([
  0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A,
  0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
]);

const MSG_PERMUTATION: number[] = [2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8];

/** 32 位加法（保持无符号） */
function add32(a: number, b: number): number {
  return (a + b) >>> 0;
}

/** 32 位右旋转 */
function rotr32(x: number, n: number): number {
  return ((x >>> n) | (x << (32 - n))) >>> 0;
}

/** G 混合函数：修改 state[a/b/c/d]，输入两个消息字 mx, my */
function g(state: Uint32Array, a: number, b: number, c: number, d: number, mx: number, my: number): void {
  state[a] = add32(add32(state[a], state[b]), mx);
  state[d] = rotr32(state[d] ^ state[a], 16);
  state[c] = add32(state[c], state[d]);
  state[b] = rotr32(state[b] ^ state[c], 12);
  state[a] = add32(add32(state[a], state[b]), my);
  state[d] = rotr32(state[d] ^ state[a], 8);
  state[c] = add32(state[c], state[d]);
  state[b] = rotr32(state[b] ^ state[c], 7);
}

/** 一轮混合：state 与 16 字消息字 m */
function roundState(state: Uint32Array, m: Uint32Array): void {
  // Mix the columns.
  g(state, 0, 4, 8, 12, m[0], m[1]);
  g(state, 1, 5, 9, 13, m[2], m[3]);
  g(state, 2, 6, 10, 14, m[4], m[5]);
  g(state, 3, 7, 11, 15, m[6], m[7]);
  // Mix the diagonals.
  g(state, 0, 5, 10, 15, m[8], m[9]);
  g(state, 1, 6, 11, 12, m[10], m[11]);
  g(state, 2, 7, 8, 13, m[12], m[13]);
  g(state, 3, 4, 9, 14, m[14], m[15]);
}

/** 对 16 字消息字做置换（原地修改 m） */
function permute(m: Uint32Array): void {
  const permuted: Uint32Array = new Uint32Array(16);
  for (let i = 0; i < 16; i++) {
    permuted[i] = m[MSG_PERMUTATION[i]];
  }
  for (let i = 0; i < 16; i++) {
    m[i] = permuted[i];
  }
}

/**
 * 核心压缩函数。
 * 返回 16 个 u32 字，前 8 字为 chaining value，后 8 字为另一半（输出用）。
 */
function compress(
  chainingValue: Uint32Array,
  blockWords: Uint32Array,
  counter: number,
  blockLen: number,
  flags: number
): Uint32Array {
  const counterLow: number = (counter & 0xFFFFFFFF) >>> 0;
  const counterHigh: number = (Math.floor(counter / 0x100000000)) >>> 0;
  const state: Uint32Array = new Uint32Array(16);
  state[0] = chainingValue[0];
  state[1] = chainingValue[1];
  state[2] = chainingValue[2];
  state[3] = chainingValue[3];
  state[4] = chainingValue[4];
  state[5] = chainingValue[5];
  state[6] = chainingValue[6];
  state[7] = chainingValue[7];
  state[8] = IV[0];
  state[9] = IV[1];
  state[10] = IV[2];
  state[11] = IV[3];
  state[12] = counterLow;
  state[13] = counterHigh;
  state[14] = blockLen;
  state[15] = flags;

  const block: Uint32Array = new Uint32Array(16);
  for (let i = 0; i < 16; i++) {
    block[i] = blockWords[i];
  }

  roundState(state, block); // 1
  permute(block);
  roundState(state, block); // 2
  permute(block);
  roundState(state, block); // 3
  permute(block);
  roundState(state, block); // 4
  permute(block);
  roundState(state, block); // 5
  permute(block);
  roundState(state, block); // 6
  permute(block);
  roundState(state, block); // 7

  for (let i = 0; i < 8; i++) {
    state[i] = (state[i] ^ state[i + 8]) >>> 0;
    state[i + 8] = (state[i + 8] ^ chainingValue[i]) >>> 0;
  }
  return state;
}

function first8Words(compressionOutput: Uint32Array): Uint32Array {
  const out: Uint32Array = new Uint32Array(8);
  for (let i = 0; i < 8; i++) {
    out[i] = compressionOutput[i];
  }
  return out;
}

/** 把小端字节序列读成 u32 数组 */
function wordsFromLittleEndianBytes(bytes: Uint8Array, bytesLen: number): Uint32Array {
  const wordCount: number = Math.floor(bytesLen / 4);
  const words: Uint32Array = new Uint32Array(wordCount);
  for (let i = 0; i < wordCount; i++) {
    const b0: number = bytes[i * 4];
    const b1: number = bytes[i * 4 + 1];
    const b2: number = bytes[i * 4 + 2];
    const b3: number = bytes[i * 4 + 3];
    words[i] = ((b0) | (b1 << 8) | (b2 << 16) | (b3 << 24)) >>> 0;
  }
  return words;
}

/**
 * Output 表示「压缩前的状态快照」：可派生 chaining value 或 root 输出。
 */
class Output {
  inputChainingValue: Uint32Array;
  blockWords: Uint32Array;
  counter: number;
  blockLen: number;
  flags: number;

  constructor(
    inputChainingValue: Uint32Array,
    blockWords: Uint32Array,
    counter: number,
    blockLen: number,
    flags: number
  ) {
    this.inputChainingValue = inputChainingValue;
    this.blockWords = blockWords;
    this.counter = counter;
    this.blockLen = blockLen;
    this.flags = flags;
  }

  chainingValue(): Uint32Array {
    return first8Words(compress(this.inputChainingValue, this.blockWords, this.counter, this.blockLen, this.flags));
  }

  /** 生成 root 输出字节（任意长度，每 64 字节为一个输出块） */
  rootOutputBytes(outLen: number): Uint8Array {
    const out: Uint8Array = new Uint8Array(outLen);
    let outputBlockCounter: number = 0;
    let written: number = 0;
    while (written < outLen) {
      const words: Uint32Array = compress(
        this.inputChainingValue,
        this.blockWords,
        outputBlockCounter,
        this.blockLen,
        this.flags | ROOT
      );
      const remaining: number = outLen - written;
      const take: number = Math.min(remaining, 2 * OUT_LEN); // 每 64 字节一个块
      for (let i = 0; i < take; i++) {
        const wordIdx: number = Math.floor(i / 4);
        const byteIdx: number = i % 4;
        const word: number = words[wordIdx];
        out[written + i] = (word >>> (byteIdx * 8)) & 0xFF;
      }
      written += take;
      outputBlockCounter += 1;
    }
    return out;
  }
}

/**
 * ChunkState —— 累积单个 chunk 的 1024 字节内的多个 64 字节块。
 */
class ChunkState {
  chainingValue: Uint32Array;
  chunkCounter: number;
  block: Uint8Array;
  blockLen: number; // 当前块已写字节数 0..64
  blocksCompressed: number;
  flags: number;

  constructor(keyWords: Uint32Array, chunkCounter: number, flags: number) {
    this.chainingValue = keyWords;
    this.chunkCounter = chunkCounter;
    this.block = new Uint8Array(BLOCK_LEN);
    this.blockLen = 0;
    this.blocksCompressed = 0;
    this.flags = flags;
  }

  /** 当前 chunk 已积累的字节数 */
  len(): number {
    return BLOCK_LEN * this.blocksCompressed + this.blockLen;
  }

  startFlag(): number {
    return this.blocksCompressed === 0 ? CHUNK_START : 0;
  }

  update(input: Uint8Array): void {
    let offset: number = 0;
    while (offset < input.length) {
      if (this.blockLen === BLOCK_LEN) {
        // block 满，压缩并清空
        const blockWords: Uint32Array = wordsFromLittleEndianBytes(this.block, BLOCK_LEN);
        this.chainingValue = first8Words(compress(
          this.chainingValue,
          blockWords,
          this.chunkCounter,
          BLOCK_LEN,
          this.flags | this.startFlag()
        ));
        this.blocksCompressed += 1;
        this.block = new Uint8Array(BLOCK_LEN);
        this.blockLen = 0;
      }
      const want: number = BLOCK_LEN - this.blockLen;
      const take: number = Math.min(want, input.length - offset);
      for (let i = 0; i < take; i++) {
        this.block[this.blockLen + i] = input[offset + i];
      }
      this.blockLen += take;
      offset += take;
    }
  }

  output(): Output {
    const blockWords: Uint32Array = wordsFromLittleEndianBytes(this.block, BLOCK_LEN);
    // blockWords 是 16 个 u32，但当 block 未填满时后面是 0 —— 与参考实现行为一致
    return new Output(
      this.chainingValue,
      blockWords,
      this.chunkCounter,
      this.blockLen,
      this.flags | this.startFlag() | CHUNK_END
    );
  }
}

/** 构造 parent Output：block = left_child_cv ‖ right_child_cv */
function parentOutput(
  leftChildCv: Uint32Array,
  rightChildCv: Uint32Array,
  keyWords: Uint32Array,
  flags: number
): Output {
  const blockWords: Uint32Array = new Uint32Array(16);
  for (let i = 0; i < 8; i++) {
    blockWords[i] = leftChildCv[i];
    blockWords[i + 8] = rightChildCv[i];
  }
  return new Output(keyWords, blockWords, 0, BLOCK_LEN, PARENT | flags);
}

function parentCv(
  leftChildCv: Uint32Array,
  rightChildCv: Uint32Array,
  keyWords: Uint32Array,
  flags: number
): Uint32Array {
  return parentOutput(leftChildCv, rightChildCv, keyWords, flags).chainingValue();
}

/**
 * Blake3Hasher —— 增量式哈希器，支持分块 update。
 */
export class Blake3Hasher {
  private chunkState: ChunkState;
  private keyWords: Uint32Array;
  private cvStack: Uint32Array[]; // 每元素 8 个 u32
  private cvStackLen: number;
  private flags: number;

  private constructor(keyWords: Uint32Array, flags: number) {
    this.keyWords = keyWords;
    this.flags = flags;
    this.chunkState = new ChunkState(keyWords, 0, flags);
    this.cvStack = [];
    this.cvStackLen = 0;
  }

  static new(): Blake3Hasher {
    return new Blake3Hasher(IV, 0);
  }

  private pushStack(cv: Uint32Array): void {
    // 扩容数组
    this.cvStack[this.cvStackLen] = cv;
    this.cvStackLen += 1;
  }

  private popStack(): Uint32Array {
    this.cvStackLen -= 1;
    const cv: Uint32Array = this.cvStack[this.cvStackLen];
    this.cvStack[this.cvStackLen] = new Uint32Array(0); // help gc
    return cv;
  }

  private addChunkChainingValue(newCv: Uint32Array, totalChunks: number): void {
    let cv: Uint32Array = newCv;
    let total: number = totalChunks;
    while ((total & 1) === 0) {
      cv = parentCv(this.popStack(), cv, this.keyWords, this.flags);
      total = total >>> 1;
    }
    this.pushStack(cv);
  }

  /** 累积输入字节 */
  update(input: Uint8Array): void {
    let offset: number = 0;
    while (offset < input.length) {
      if (this.chunkState.len() === CHUNK_LEN) {
        const chunkCv: Uint32Array = this.chunkState.output().chainingValue();
        const totalChunks: number = this.chunkState.chunkCounter + 1;
        this.addChunkChainingValue(chunkCv, totalChunks);
        this.chunkState = new ChunkState(this.keyWords, totalChunks, this.flags);
      }
      const want: number = CHUNK_LEN - this.chunkState.len();
      const take: number = Math.min(want, input.length - offset);
      const slice: Uint8Array = input.subarray(offset, offset + take);
      this.chunkState.update(slice);
      offset += take;
    }
  }

  /** 结束并写出 outLen 字节（标准用法 outLen=32） */
  finalize(outLen: number = OUT_LEN): Uint8Array {
    let output: Output = this.chunkState.output();
    let parentNodesRemaining: number = this.cvStackLen;
    while (parentNodesRemaining > 0) {
      parentNodesRemaining -= 1;
      const leftCv: Uint32Array = this.cvStack[parentNodesRemaining];
      output = parentOutput(leftCv, output.chainingValue(), this.keyWords, this.flags);
    }
    return output.rootOutputBytes(outLen);
  }
}

/**
 * 标准 BLAKE3 哈希（32 字节）。
 */
export function blake3Hash(bytes: Uint8Array): Uint8Array {
  const hasher: Blake3Hasher = Blake3Hasher.new();
  hasher.update(bytes);
  return hasher.finalize(OUT_LEN);
}

/** 32 字节哈希 → 64 字符 hex（小写） */
export function bytesToHex(bytes: Uint8Array): string {
  const hexChars: string = '0123456789abcdef';
  let result: string = '';
  for (let i = 0; i < bytes.length; i++) {
    const b: number = bytes[i];
    result += hexChars.charAt((b >>> 4) & 0xF);
    result += hexChars.charAt(b & 0xF);
  }
  return result;
}

export function blake3HashHex(bytes: Uint8Array): string {
  return bytesToHex(blake3Hash(bytes));
}

/**
 * 由叶子哈希数组构造 Merkle 树根（与服务端 fc_merkle_root 一致）。
 * 规则：两两 parent = blake3(left ‖ right)；某层节点数为奇数时最后一个原样进位；
 *       空数组返回 blake3("")。
 * leaves: Uint8Array，每 32 字节为一个叶子；返回 32 字节树根。
 */
export function blake3MerkleRoot(leaves: Uint8Array): Uint8Array {
  if (leaves.length === 0) {
    return blake3Hash(new Uint8Array(0));
  }
  // level：当前层的 CV 字节数组（每个 32B）
  let level: Uint8Array = leaves;
  while (level.length > 32) {
    const pairCount: number = Math.floor(level.length / 64);
    const nextLen: number = pairCount * 32 + ((level.length % 64 === 32) ? 32 : 0); // 奇数进位 32B
    const next: Uint8Array = new Uint8Array(nextLen);
    let outOffset: number = 0;
    let i: number = 0;
    while (i < level.length) {
      if (i + 32 <= level.length - 32) {
        // 有右兄弟
        const combined: Uint8Array = new Uint8Array(64);
        for (let j = 0; j < 32; j++) {
          combined[j] = level[i + j];
          combined[32 + j] = level[i + 32 + j];
        }
        const parent: Uint8Array = blake3Hash(combined);
        for (let j = 0; j < 32; j++) {
          next[outOffset + j] = parent[j];
        }
        outOffset += 32;
        i += 64;
      } else {
        // 奇数进位
        for (let j = 0; j < 32; j++) {
          next[outOffset + j] = level[i + j];
        }
        outOffset += 32;
        i += 32;
      }
    }
    level = next;
  }
  // level 现在正好 32 字节
  return level;
}

/** Merkle 树根 hex */
export function blake3MerkleRootHex(leaves: Uint8Array): string {
  return bytesToHex(blake3MerkleRoot(leaves));
}
