package synccore

/*
#include <stdlib.h>
#include "sys_core.h"
*/
import "C"

import (
	"time"
	"unsafe"
)

// 服务端分片接收器（Rust receiver）的 Go 薄封装，对应 ABI v5 的 fc_receiver_* 系列。
//
// 职责边界（见 .dsh-work/RECEIVER-ABI.md）:
//   - Go：HTTP/鉴权/会话元数据/低频 Redis 快照与重启恢复；
//   - Rust：有界内存缓存、接收位图、abandoned 判断、批量定位写、最终校验与坏块索引。
//
// 本文件所有函数只做类型/错误码转换，不做任何业务判断。
//
// 部署前提：receiver 是进程内状态，服务端必须【单 Go 进程、单实例】部署；
// 多实例时同一 upload_id 的请求落到不同进程会互不可见（需实例亲和，本次不实现）。

// ReceiverAcceptResult 一次 accept/flush 的结果快照。
type ReceiverAcceptResult struct {
	Received      int  // 已接受分片数（accepted 计数）
	Complete      bool // accepted 已覆盖全部分片
	SnapshotReady bool // dirty 且距上次快照信号 >= 快照间隔（约 1s）
	Duplicate     bool // 该片此前已接受（幂等重复）
}

func acceptResultFromC(r *C.fc_accept_result) ReceiverAcceptResult {
	return ReceiverAcceptResult{
		Received:      int(r.received),
		Complete:      r.complete != 0,
		SnapshotReady: r.snapshot_ready != 0,
		Duplicate:     r.duplicate != 0,
	}
}

// ReceiverOpen 打开/注册会话接收器。
// 返回 created=true 表示本次新建；false 表示已存在（参数一致，未覆盖原有状态）。
// persisted 为 Redis 中持久化的位图（重启恢复用），可为 nil。
func ReceiverOpen(uploadID, path string, totalSize, chunkSize uint64, chunkCount int, persisted []byte, ttl time.Duration) (created bool, err error) {
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	var ttlSecs uint64
	if ttl > 0 {
		ttlSecs = uint64(ttl / time.Second)
		if ttlSecs == 0 {
			ttlSecs = 1 // 亚秒 TTL 至少 1s，0 会被 Rust 当成「用默认 24h」
		}
	}

	rc := C.fc_receiver_open(
		cid, cpath,
		C.uint64_t(totalSize), C.uint64_t(chunkSize), C.uint64_t(uint64(chunkCount)),
		bytePtr(persisted), C.size_t(len(persisted)),
		C.uint64_t(ttlSecs),
	)
	switch int32(rc) {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, codeToErr(rc)
	}
}

// ReceiverExists 判断 receiver 是否已注册。
func ReceiverExists(uploadID string) bool {
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	return int32(C.fc_receiver_exists(cid)) == 1
}

// ReceiverAccept 接收一个分片（数据由 Rust 拷贝进 pending，Go 保留切片所有权）。
// attemptID 为空时不校验放弃状态；返回 ErrReceiverAbandoned 表示该 (index, attempt_id)
// 已被放弃，ErrReceiverNotFound 表示 receiver 不存在。
func ReceiverAccept(uploadID string, index int, data []byte, attemptID string) (ReceiverAcceptResult, error) {
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	var cattempt *C.char
	if attemptID != "" {
		cattempt = C.CString(attemptID)
		defer C.free(unsafe.Pointer(cattempt))
	}
	var out C.fc_accept_result
	rc := C.fc_receiver_accept(
		cid, C.uint32_t(uint32(index)),
		bytePtr(data), C.size_t(len(data)),
		cattempt, &out,
	)
	if err := codeToErr(rc); err != nil {
		return ReceiverAcceptResult{}, err
	}
	return acceptResultFromC(&out), nil
}

// ReceiverFlush 把 pending 按 index 排序、合并相邻片后批量定位写。
// 写盘失败返回 ErrIO（该组 pending 保留，可重试）；receiver 不存在返回 ErrReceiverNotFound。
func ReceiverFlush(uploadID string) (ReceiverAcceptResult, error) {
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	var out C.fc_accept_result
	rc := C.fc_receiver_flush(cid, &out)
	if err := codeToErr(rc); err != nil {
		return ReceiverAcceptResult{}, err
	}
	return acceptResultFromC(&out), nil
}

// receiverBitmap 导出 accepted/persisted 位图。chunkCount 决定缓冲区大小（ceil(n/8)）。
func receiverBitmap(uploadID string, chunkCount int, persisted bool) ([]byte, error) {
	need := (chunkCount + 7) / 8
	if need <= 0 {
		return []byte{}, nil
	}
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	buf := make([]byte, need)
	var rc C.int64_t
	if persisted {
		rc = C.fc_receiver_bitmap_persisted(cid, bytePtr(buf), C.size_t(len(buf)))
	} else {
		rc = C.fc_receiver_bitmap_accepted(cid, bytePtr(buf), C.size_t(len(buf)))
	}
	if rc < 0 {
		return nil, codeToErr(C.int32_t(int32(rc)))
	}
	n := int(rc)
	if n > len(buf) {
		n = len(buf)
	}
	return buf[:n], nil
}

// ReceiverBitmapAccepted 导出「已接受」位图（含仅内存、尚未落盘的片）。
// 字节序与 Redis SETBIT 一致（MSB-first）。
func ReceiverBitmapAccepted(uploadID string, chunkCount int) ([]byte, error) {
	return receiverBitmap(uploadID, chunkCount, false)
}

// ReceiverBitmapPersisted 导出「已落盘」位图（可安全写入 Redis 快照）。
func ReceiverBitmapPersisted(uploadID string, chunkCount int) ([]byte, error) {
	return receiverBitmap(uploadID, chunkCount, true)
}

// ReceiverSnapshotAck 通知 Rust：Go 已成功把 persisted 位图写入 Redis，可以清 dirty。
func ReceiverSnapshotAck(uploadID string) error {
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	return codeToErr(C.fc_receiver_snapshot_ack(cid))
}

// ReceiverFinalize flush 全部 pending 并做叶子/Merkle/整文件校验。
//
// 成功时 receiver 已被 Rust 移除（Go 随后 Move 文件），返回整文件哈希。
// ErrLeafMismatch 时 badIndex 为首个坏块（Rust 已清该片的 accepted/persisted 位、保留 receiver）。
func ReceiverFinalize(uploadID string, leaves, expectedRoot []byte) (fileHash []byte, badIndex int64, err error) {
	if len(leaves)%HashSize != 0 {
		return nil, -1, ErrArg
	}
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	out := make([]byte, HashSize)
	var bad C.int64_t = -1
	rc := C.fc_receiver_finalize(
		cid,
		bytePtr(leaves), C.size_t(len(leaves)/HashSize),
		bytePtr(expectedRoot),
		bytePtr(out), &bad,
	)
	if e := codeToErr(rc); e != nil {
		return nil, int64(bad), e
	}
	return out, int64(bad), nil
}

// ReceiverAbandon 记录放弃的 (index, attemptID)。receiver 不存在时 Rust 会缓冲，open 时合并；
// 因此 abandon 与 open 的竞态不会漏判。幂等。
func ReceiverAbandon(uploadID string, index int, attemptID string) error {
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	cattempt := C.CString(attemptID)
	defer C.free(unsafe.Pointer(cattempt))
	return codeToErr(C.fc_receiver_abandon(cid, C.uint32_t(uint32(index)), cattempt))
}

// ReceiverClose 移除 receiver（不 flush、不写盘）；幂等，不存在返回 nil。
func ReceiverClose(uploadID string) error {
	cid := C.CString(uploadID)
	defer C.free(unsafe.Pointer(cid))
	return codeToErr(C.fc_receiver_close(cid))
}
