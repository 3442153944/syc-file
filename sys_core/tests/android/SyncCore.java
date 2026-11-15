package com.sunyuanling.filesync.core;
final class SyncCore {
    static native int nativeAbiVersion();
    static native byte[] nativeHashChunk(byte[] data);
    static native byte[] nativeMerkleRoot(byte[] leaves);
    static native byte[] nativeDescribeFile(String path, long chunkSize);
}
