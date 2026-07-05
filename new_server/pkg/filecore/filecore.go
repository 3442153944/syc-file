// Package filecore 是对 Rust 核心库 filecore 的 cgo 封装。
//
// 底层实现见 new_server/file_lib（Rust，staticlib）。构建产物为
// file_lib/lib/libfilecore.a，需先运行 file_lib/build.ps1 生成。
//
// 所有哈希均为 32 字节 blake3。Go 侧只做编排（HTTP/鉴权/Redis 会话/DB），
// 预分配、乱序定位写、分片与 Merkle 校验、整文件哈希等热路径全在 Rust。
package filecore

/*
#cgo CFLAGS: -I${SRCDIR}/../../file_lib
#cgo LDFLAGS: -L${SRCDIR}/../../file_lib/lib -lfilecore -lkernel32 -lntdll -luserenv -lws2_32 -ldbghelp
#include <stdlib.h>
#include "filecore.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// HashSize 是 blake3 哈希字节数。
const HashSize = 32

// 与 Rust/filecore.h 对应的错误。
var (
	ErrArg          = errors.New("filecore: 参数无效")
	ErrIO           = errors.New("filecore: IO 错误")
	ErrLeafMismatch = errors.New("filecore: 分片哈希不匹配")
	ErrRootMismatch = errors.New("filecore: Merkle 树根不匹配")
	ErrUnknown      = errors.New("filecore: 未知错误")
)

func codeToErr(rc C.int32_t) error {
	switch int32(rc) {
	case C.FC_OK:
		return nil
	case C.FC_ERR_ARG:
		return ErrArg
	case C.FC_ERR_IO:
		return ErrIO
	case C.FC_ERR_LEAF_MISMATCH:
		return ErrLeafMismatch
	case C.FC_ERR_ROOT_MISMATCH:
		return ErrRootMismatch
	default:
		return ErrUnknown
	}
}

// bytePtr 返回切片首元素指针（空切片返回 nil），供传给 C。
func bytePtr(b []byte) *C.uint8_t {
	if len(b) == 0 {
		return nil
	}
	return (*C.uint8_t)(unsafe.Pointer(&b[0]))
}

// ABIVersion 返回 Rust 库的 ABI 版本，用于链接自检。
func ABIVersion() int {
	return int(C.fc_abi_version())
}

// Preallocate 预分配临时文件到 totalSize 大小。
func Preallocate(path string, totalSize uint64) error {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	return codeToErr(C.fc_preallocate(cp, C.uint64_t(totalSize)))
}

// ChunkWrite 将 data 写入临时文件的 offset 处。expectedLeaf 非 nil（长度须为
// HashSize）时先做 blake3 早校验，不匹配返回 ErrLeafMismatch 且不落盘。
func ChunkWrite(path string, offset uint64, data, expectedLeaf []byte) error {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var leaf *C.uint8_t
	if len(expectedLeaf) == HashSize {
		leaf = bytePtr(expectedLeaf)
	}
	rc := C.fc_chunk_write(cp, C.uint64_t(offset), bytePtr(data), C.size_t(len(data)), leaf)
	return codeToErr(rc)
}

// HashChunk 计算一段数据的 blake3 叶子哈希。
func HashChunk(data []byte) ([]byte, error) {
	out := make([]byte, HashSize)
	rc := C.fc_hash_chunk(bytePtr(data), C.size_t(len(data)), bytePtr(out))
	if err := codeToErr(rc); err != nil {
		return nil, err
	}
	return out, nil
}

// MerkleRoot 从叶子哈希（leaves 为若干 32 字节哈希拼接）构造 Merkle 树根。
func MerkleRoot(leaves []byte) ([]byte, error) {
	if len(leaves)%HashSize != 0 {
		return nil, ErrArg
	}
	out := make([]byte, HashSize)
	rc := C.fc_merkle_root(bytePtr(leaves), C.size_t(len(leaves)/HashSize), bytePtr(out))
	if err := codeToErr(rc); err != nil {
		return nil, err
	}
	return out, nil
}

// Finalize 对临时文件做整体校验：按 chunkSize 逐块重算，若 expectedLeaves 非空则
// 逐块比对，再用 expectedRoot 校验 Merkle 树根，并算出整文件哈希。
//
// 返回：整文件哈希、坏块索引（无坏块为 -1）、错误。
// err 为 ErrLeafMismatch 时 badIndex 指向首个坏块。
func Finalize(path string, chunkSize, totalSize uint64, expectedLeaves, expectedRoot []byte) (fileHash []byte, badIndex int64, err error) {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))

	if len(expectedLeaves)%HashSize != 0 {
		return nil, -1, ErrArg
	}
	leafCount := len(expectedLeaves) / HashSize

	out := make([]byte, HashSize)
	var bad C.int64_t = -1

	rc := C.fc_finalize(
		cp,
		C.uint64_t(chunkSize),
		C.uint64_t(totalSize),
		bytePtr(expectedLeaves),
		C.size_t(leafCount),
		bytePtr(expectedRoot),
		bytePtr(out),
		&bad,
	)
	if err := codeToErr(rc); err != nil {
		return nil, int64(bad), err
	}
	return out, int64(bad), nil
}

// Move 原子落盘：把临时文件移动到最终路径。
func Move(src, dst string) error {
	cs := C.CString(src)
	defer C.free(unsafe.Pointer(cs))
	cd := C.CString(dst)
	defer C.free(unsafe.Pointer(cd))
	return codeToErr(C.fc_move(cs, cd))
}
