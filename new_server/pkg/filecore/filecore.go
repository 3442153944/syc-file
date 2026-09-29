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
#cgo LDFLAGS: -L${SRCDIR}/../../file_lib/lib -lfilecore -lkernel32 -lntdll -luserenv -lws2_32 -ldbghelp -liphlpapi -lpdh -lole32 -loleaut32 -lpropsys -lruntimeobject -lpsapi -lsecur32 -lnetapi32 -lpowrprof
#include <stdlib.h>
#include "filecore.h"
*/
import "C"

import (
	"encoding/json"
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
	ErrSizeMismatch = errors.New("filecore: 文件尺寸/分片数与描述不符")
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
	case C.FC_ERR_SIZE_MISMATCH:
		return ErrSizeMismatch
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

// Evict 逐出并关闭 Rust 侧缓存的该路径写句柄。
//
// 在 Go 侧删除/移动临时文件（os.Remove 等）前必须先调用，否则缓存可能残留
// 指向已删文件的句柄，同路径新会话的分片写入会静默丢失。
// Preallocate/Finalize/Move 内部已自带逐出，无须额外调用。
func Evict(path string) error {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	return codeToErr(C.fc_evict(cp))
}

// ProcessInfo 一个进程的资源占用快照。Score 是排序用的加权综合分
//（cpu*2 + mem*1.5 + 连接数*1，各自在本轮进程集合内归一化，见 sys_info.rs），
// 网络维度缺跨平台可靠的进程级字节数接口，用连接数代理。
type ProcessInfo struct {
	PID            uint32  `json:"pid"`
	Name           string  `json:"name"`
	CPUPercent     float32 `json:"cpu_percent"`
	MemBytes       uint64  `json:"mem_bytes"`
	MemPercent     float32 `json:"mem_percent"`
	DiskReadBytes  uint64  `json:"disk_read_bytes"`
	DiskWriteBytes uint64  `json:"disk_write_bytes"`
	Connections    uint32  `json:"connections"`
	Score          float32 `json:"score"`
}

// ListeningPort 一个正在监听的端口。
type ListeningPort struct {
	Port        uint16 `json:"port"`
	Protocol    string `json:"protocol"` // "tcp" | "udp"
	PID         uint32 `json:"pid"`
	ProcessName string `json:"process_name"`
}

// PortConnCount 某个端口/协议当前的连接数（不含每条连接的明细）。
type PortConnCount struct {
	Port        uint16 `json:"port"`
	Protocol    string `json:"protocol"`
	Connections uint32 `json:"connections"`
}

// SysSnapshot 一次系统明细采集的结果。
type SysSnapshot struct {
	Processes       []ProcessInfo   `json:"processes"`
	ListeningPorts  []ListeningPort `json:"listening_ports"`
	PortConnections []PortConnCount `json:"port_connections"`
	// 本轮归一化 cpu_percent 用的逻辑核数，见 sys_info.rs 的注释。
	NumCpus uint32 `json:"num_cpus"`
}

// CollectSysSnapshot 采一次进程 Top-N + 端口/连接明细。topN 决定返回的进程
// 条数（按综合评分排序取前 N 个），端口/连接明细不受 topN 限制。
func CollectSysSnapshot(topN int) (*SysSnapshot, error) {
	var cJSON *C.char
	rc := C.fc_sys_snapshot(C.uint32_t(topN), &cJSON)
	if err := codeToErr(rc); err != nil {
		return nil, err
	}
	defer C.fc_free_string(cJSON)

	var snap SysSnapshot
	if err := json.Unmarshal([]byte(C.GoString(cJSON)), &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
