/*
 * filecore NAPI wrapper for HarmonyOS.
 *
 * 所有文件与上传实现来自仓库根 sys_core；本文件只负责 NAPI 参数封送。
 * ArkTS 侧通过 `import filecore from 'libfilecore.so'` 调用本模块导出的函数。
 *
 * 导出（对标 Android filecore_jni/src/lib.rs 的 JNI 函数）：
 *   - abiVersion(): number
 *   - hashChunk(data: Uint8Array): Uint8Array | null     (32 bytes blake3)
 *   - merkleRoot(leaves: Uint8Array): Uint8Array | null   (32 bytes，leaves 长度须为 32 倍数)
 *   - describeFile(path: string, chunkSize: number): Uint8Array | null
 *       返回 packed [file_hash(32) || merkle_root(32) || leaves(n*32)]，失败返回 null。
 *
 * 文件函数失败返回 null；上传 start 失败返回 0，reply 返回 C ABI 状态码。
 * sys_core 内所有导出已包 ffi_guard（catch_unwind），panic 不会跨 FFI 展开；
 * 此处再防一道参数校验。
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

bool ReadString(napi_env env, napi_value val, std::string* out) {
    size_t len = 0;
    if (napi_get_value_string_utf8(env, val, nullptr, 0, &len) != napi_ok) return false;
    std::vector<char> buf(len + 1, 0);
    if (napi_get_value_string_utf8(env, val, buf.data(), buf.size(), &len) != napi_ok) return false;
    *out = std::string(buf.data(), len);
    return true;
}

bool ReadU64(napi_env env, napi_value val, uint64_t* out) {
    double value = 0;
    if (napi_get_value_double(env, val, &value) != napi_ok) return false;
    if (value != value || value < 0 || value > 9007199254740991.0 ||
        static_cast<double>(static_cast<uint64_t>(value)) != value) {
        return false;
    }
    *out = static_cast<uint64_t>(value);
    return true;
}

napi_value MakeU64(napi_env env, uint64_t value) {
    napi_value result = nullptr;
    napi_create_double(env, static_cast<double>(value), &result);
    return result;
}

napi_value MakeI32(napi_env env, int32_t value) {
    napi_value result = nullptr;
    napi_create_int32(env, value, &result);
    return result;
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

napi_value StartUpload(napi_env env, napi_callback_info info) {
    size_t argc = 1;
    napi_value args[1];
    if (napi_get_cb_info(env, info, &argc, args, nullptr, nullptr) != napi_ok || argc < 1) {
        return MakeU64(env, 0);
    }
    std::string input;
    if (!ReadString(env, args[0], &input)) return MakeU64(env, 0);
    return MakeU64(env, fc_upload_start(input.c_str()));
}

napi_value PollUpload(napi_env env, napi_callback_info info) {
    size_t argc = 1;
    napi_value args[1];
    uint64_t id = 0;
    if (napi_get_cb_info(env, info, &argc, args, nullptr, nullptr) != napi_ok ||
        argc < 1 || !ReadU64(env, args[0], &id)) {
        return NullValue(env);
    }
    char* events = fc_upload_poll(id);
    if (events == nullptr) return NullValue(env);
    napi_value result = nullptr;
    napi_status status = napi_create_string_utf8(env, events, NAPI_AUTO_LENGTH, &result);
    fc_free_string(events);
    return status == napi_ok ? result : NullValue(env);
}

napi_value TakeUploadBody(napi_env env, napi_callback_info info) {
    size_t argc = 2;
    napi_value args[2];
    uint64_t id = 0;
    uint64_t request = 0;
    if (napi_get_cb_info(env, info, &argc, args, nullptr, nullptr) != napi_ok || argc < 2 ||
        !ReadU64(env, args[0], &id) || !ReadU64(env, args[1], &request)) {
        return NullValue(env);
    }
    uint8_t* data = nullptr;
    size_t len = 0;
    if (fc_upload_take_body(id, request, &data, &len) != FC_OK) return NullValue(env);
    napi_value result = MakeBytes(env, data, len);
    fc_upload_body_free(data, len);
    return result;
}

napi_value ReplyUpload(napi_env env, napi_callback_info info) {
    size_t argc = 3;
    napi_value args[3];
    uint64_t id = 0;
    uint64_t request = 0;
    std::string result;
    if (napi_get_cb_info(env, info, &argc, args, nullptr, nullptr) != napi_ok || argc < 3 ||
        !ReadU64(env, args[0], &id) || !ReadU64(env, args[1], &request) ||
        !ReadString(env, args[2], &result)) {
        return MakeI32(env, FC_ERR_ARG);
    }
    return MakeI32(env, fc_upload_reply(id, request, result.c_str()));
}

napi_value CancelUpload(napi_env env, napi_callback_info info) {
    size_t argc = 1;
    napi_value args[1];
    uint64_t id = 0;
    if (napi_get_cb_info(env, info, &argc, args, nullptr, nullptr) == napi_ok &&
        argc >= 1 && ReadU64(env, args[0], &id)) {
        fc_upload_cancel(id);
    }
    napi_value result = nullptr;
    napi_get_undefined(env, &result);
    return result;
}

napi_value Init(napi_env env, napi_value exports) {
    napi_property_descriptor props[] = {
        {"abiVersion",     nullptr, AbiVersion,     nullptr, nullptr, nullptr, napi_default, nullptr},
        {"hashChunk",      nullptr, HashChunk,      nullptr, nullptr, nullptr, napi_default, nullptr},
        {"merkleRoot",     nullptr, MerkleRoot,     nullptr, nullptr, nullptr, napi_default, nullptr},
        {"describeFile",   nullptr, DescribeFile,   nullptr, nullptr, nullptr, napi_default, nullptr},
        {"startUpload",    nullptr, StartUpload,    nullptr, nullptr, nullptr, napi_default, nullptr},
        {"pollUpload",     nullptr, PollUpload,     nullptr, nullptr, nullptr, napi_default, nullptr},
        {"takeUploadBody", nullptr, TakeUploadBody, nullptr, nullptr, nullptr, napi_default, nullptr},
        {"replyUpload",    nullptr, ReplyUpload,    nullptr, nullptr, nullptr, napi_default, nullptr},
        {"cancelUpload",   nullptr, CancelUpload,   nullptr, nullptr, nullptr, napi_default, nullptr},
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
