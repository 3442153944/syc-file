/*
 * filecore NAPI wrapper for HarmonyOS.
 *
 * 与服务端 file_lib（Rust libfilecore.a）/ 桌面端 Rust blake3 / Android filecore_jni 逐字节一致。
 * ArkTS 侧通过 `import filecore from 'libfilecore.so'` 调用本模块导出的函数。
 *
 * 导出（对标 Android filecore_jni/src/lib.rs 的 JNI 函数）：
 *   - abiVersion(): number
 *   - hashChunk(data: Uint8Array): Uint8Array | null     (32 bytes blake3)
 *   - merkleRoot(leaves: Uint8Array): Uint8Array | null   (32 bytes，leaves 长度须为 32 倍数)
 *   - describeFile(path: string, chunkSize: number): Uint8Array | null
 *       返回 packed [file_hash(32) || merkle_root(32) || leaves(n*32)]，
 *       失败返回 null，ArkTS 侧回退纯 ArkTS Blake3Util（仅性能差）。
 *
 * 失败一律返回 null（对标 Android JNI 失败语义）。file_lib 内所有导出已包 ffi_guard
 * （catch_unwind），panic 不会跨 FFI 展开；此处再防一道参数校验。
 */

#include "napi/native_api.h"
#include "filecore.h"

#include <cstdint>
#include <cstring>
#include <string>
#include <sys/stat.h>
#include <vector>

namespace {

constexpr size_t kHashSize = 32;

napi_value MakeBytes(napi_env env, const uint8_t* data, size_t len) {
    void* dst = nullptr;
    napi_value arraybuffer = nullptr;
    if (napi_create_arraybuffer(env, len, &dst, &arraybuffer) != napi_ok) {
        return nullptr;
    }
    if (data != nullptr && len > 0) {
        memcpy(dst, data, len);
    }
    napi_value result = nullptr;
    if (napi_create_typedarray(env, napi_uint8_array, len, arraybuffer, 0, &result) != napi_ok) {
        return nullptr;
    }
    return result;
}

bool ReadBytes(napi_env env, napi_value val, uint8_t** out_data, size_t* out_len) {
    bool is_typedarray = false;
    if (napi_is_typedarray(env, val, &is_typedarray) != napi_ok) return false;
    if (!is_typedarray) return false;
    napi_typedarray_type array_type;
    size_t length = 0;
    void* data = nullptr;
    napi_value arraybuffer = nullptr;
    size_t offset = 0;
    if (napi_get_typedarray_info(env, val, &array_type, &length, &data, &arraybuffer, &offset) != napi_ok) {
        return false;
    }
    if (array_type != napi_uint8_array) return false;
    *out_data = reinterpret_cast<uint8_t*>(data);
    *out_len = length;
    return true;
}

napi_value NullValue(napi_env env) {
    napi_value v = nullptr;
    napi_get_null(env, &v);
    return v;
}

napi_value AbiVersion(napi_env env, napi_callback_info info) {
    int32_t v = fc_abi_version();
    napi_value result = nullptr;
    napi_create_int32(env, v, &result);
    return result;
}

napi_value HashChunk(napi_env env, napi_callback_info info) {
    size_t argc = 1;
    napi_value args[1];
    if (napi_get_cb_info(env, info, &argc, args, nullptr, nullptr) != napi_ok) {
        return NullValue(env);
    }
    if (argc < 1) return NullValue(env);

    uint8_t* data = nullptr;
    size_t len = 0;
    if (!ReadBytes(env, args[0], &data, &len)) return NullValue(env);

    uint8_t out[kHashSize];
    int32_t rc = fc_hash_chunk(data, len, out);
    if (rc != FC_OK) return NullValue(env);
    return MakeBytes(env, out, kHashSize);
}

napi_value MerkleRoot(napi_env env, napi_callback_info info) {
    size_t argc = 1;
    napi_value args[1];
    if (napi_get_cb_info(env, info, &argc, args, nullptr, nullptr) != napi_ok) {
        return NullValue(env);
    }
    if (argc < 1) return NullValue(env);

    uint8_t* leaves = nullptr;
    size_t len = 0;
    if (!ReadBytes(env, args[0], &leaves, &len)) return NullValue(env);
    if (len % kHashSize != 0) return NullValue(env);
    size_t count = len / kHashSize;

    uint8_t out[kHashSize];
    const uint8_t* leaves_ptr = (count == 0) ? nullptr : leaves;
    int32_t rc = fc_merkle_root(leaves_ptr, count, out);
    if (rc != FC_OK) return NullValue(env);
    return MakeBytes(env, out, kHashSize);
}

napi_value DescribeFile(napi_env env, napi_callback_info info) {
    size_t argc = 2;
    napi_value args[2];
    if (napi_get_cb_info(env, info, &argc, args, nullptr, nullptr) != napi_ok) {
        return NullValue(env);
    }
    if (argc < 2) return NullValue(env);

    // arg0: path (string)
    size_t path_len = 0;
    if (napi_get_value_string_utf8(env, args[0], nullptr, 0, &path_len) != napi_ok) {
        return NullValue(env);
    }
    std::vector<char> path_buf(path_len + 1, 0);
    if (napi_get_value_string_utf8(env, args[0], path_buf.data(), path_buf.size(), &path_len) != napi_ok) {
        return NullValue(env);
    }

    // arg1: chunk_size (number)
    double cs_dbl = 0;
    if (napi_get_value_double(env, args[1], &cs_dbl) != napi_ok) return NullValue(env);
    if (cs_dbl <= 0) return NullValue(env);
    uint64_t chunk_size = static_cast<uint64_t>(cs_dbl);

    // 估算容量：当前文件大小 / chunk_size + 8，算不了一次扩容重试
    struct stat st;
    size_t cap = 8;
    if (stat(path_buf.data(), &st) == 0) {
        cap = static_cast<size_t>((static_cast<uint64_t>(st.st_size) + chunk_size - 1) / chunk_size) + 8;
    }

    for (int attempt = 0; attempt < 2; attempt++) {
        std::vector<uint8_t> leaves(cap * kHashSize, 0);
        uint8_t root[kHashSize] = {0};
        uint8_t file_hash[kHashSize] = {0};

        int64_t n = fc_describe(path_buf.data(), chunk_size,
                                leaves.data(), cap,
                                root, file_hash);
        if (n == static_cast<int64_t>(FC_ERR_ARG)) {
            cap *= 2;
            continue;  // 文件比 stat 时更大：扩容重试
        }
        if (n < 0) return NullValue(env);

        size_t count = static_cast<size_t>(n);
        std::vector<uint8_t> packed;
        packed.reserve(2 * kHashSize + count * kHashSize);
        packed.insert(packed.end(), file_hash, file_hash + kHashSize);
        packed.insert(packed.end(), root, root + kHashSize);
        packed.insert(packed.end(), leaves.data(), leaves.data() + count * kHashSize);
        return MakeBytes(env, packed.data(), packed.size());
    }
    return NullValue(env);
}

napi_value Init(napi_env env, napi_value exports) {
    napi_property_descriptor props[] = {
        {"abiVersion",   nullptr, AbiVersion,   nullptr, nullptr, nullptr, napi_default, nullptr},
        {"hashChunk",    nullptr, HashChunk,    nullptr, nullptr, nullptr, napi_default, nullptr},
        {"merkleRoot",   nullptr, MerkleRoot,   nullptr, nullptr, nullptr, napi_default, nullptr},
        {"describeFile", nullptr, DescribeFile, nullptr, nullptr, nullptr, napi_default, nullptr},
    };
    napi_define_properties(env, exports, sizeof(props) / sizeof(props[0]), props);
    return exports;
}

}  // namespace

napi_module filecore_module = {
    .nm_version = 1,
    .nm_flags = 0,
    .nm_filename = nullptr,
    .nm_register_func = Init,
    .nm_modname = "filecore",
    .nm_priv = nullptr,
    .reserved = {0},
};

extern "C" __attribute__((constructor)) void RegisterFilecoreModule(void) {
    napi_module_register(&filecore_module);
}
