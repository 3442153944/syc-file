package synccore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 本文件直接对 ABI v5 的 receiver 封装做链接级验收（不经过 HTTP/handler），
// 用来尽早暴露 cgo 签名/结构体布局/错误码映射问题。

func uniqueReceiverID(t *testing.T) string {
	t.Helper()
	return "go-receiver-test-" + t.Name() + "-" + time.Now().Format("150405.000000000")
}

func chunkedLeaves(t *testing.T, data []byte, chunkSize int) (leaves []byte, root []byte, chunks [][]byte) {
	t.Helper()
	for off := 0; off < len(data); off += chunkSize {
		end := off + chunkSize
		if end > len(data) {
			end = len(data)
		}
		leaf, err := HashChunk(data[off:end])
		if err != nil {
			t.Fatalf("HashChunk: %v", err)
		}
		leaves = append(leaves, leaf...)
		chunks = append(chunks, data[off:end])
	}
	root, err := MerkleRoot(leaves)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}
	return leaves, root, chunks
}

// TestReceiverAcceptFlushFinalize 覆盖：open/exists、乱序 accept、duplicate、write-through 落盘位图、
// flush 收尾（全量回退时才真正需要它）、snapshot ack、abandoned 优先级、finalize 成功移除 receiver。
func TestReceiverAcceptFlushFinalize(t *testing.T) {
	const chunkSize = 1024
	const total = 3*chunkSize + 100 // 4 片，末片短
	data := make([]byte, total)
	for i := range data {
		data[i] = byte(i*31 + 7)
	}
	leaves, root, chunks := chunkedLeaves(t, data, chunkSize)

	dir := t.TempDir()
	tmp := filepath.Join(dir, "accept.part")
	if err := Preallocate(tmp, uint64(total)); err != nil {
		t.Fatalf("Preallocate: %v", err)
	}
	// 期望的整文件哈希：同内容另写一份，用现有 fc_finalize 算
	baseline := filepath.Join(dir, "baseline.bin")
	if err := os.WriteFile(baseline, data, 0o644); err != nil {
		t.Fatal(err)
	}
	wantHash, _, err := Finalize(baseline, chunkSize, total, nil, nil)
	if err != nil {
		t.Fatalf("Finalize baseline: %v", err)
	}

	id := uniqueReceiverID(t)
	created, err := ReceiverOpen(id, tmp, uint64(total), chunkSize, 4, nil, time.Hour)
	if err != nil || !created {
		t.Fatalf("ReceiverOpen: created=%v err=%v", created, err)
	}
	if !ReceiverExists(id) {
		t.Fatal("ReceiverExists = false")
	}
	// 再次 open：已存在返回 created=false 且不覆盖状态
	if created, err := ReceiverOpen(id, tmp, uint64(total), chunkSize, 4, nil, time.Hour); err != nil || created {
		t.Fatalf("重复 ReceiverOpen: created=%v err=%v", created, err)
	}

	// 乱序接受 2、0
	for _, idx := range []int{2, 0} {
		res, err := ReceiverAccept(id, idx, chunks[idx], "")
		if err != nil {
			t.Fatalf("ReceiverAccept[%d]: %v", idx, err)
		}
		if res.Duplicate {
			t.Fatalf("首传不应 duplicate")
		}
		if res.Complete {
			t.Fatalf("未收齐不应 complete")
		}
	}
	// 重复接受 0：duplicate=1 且已接受数不重复计
	res, err := ReceiverAccept(id, 0, chunks[0], "")
	if err != nil || !res.Duplicate || res.Received != 2 {
		t.Fatalf("重复 accept: %+v err=%v", res, err)
	}

	// write-through：accept 成功即已定位写盘，persisted 位图应与 accepted 一致，不依赖 flush。
	accepted, err := ReceiverBitmapAccepted(id, 4)
	if err != nil || len(accepted) != 1 {
		t.Fatalf("ReceiverBitmapAccepted: len=%d err=%v", len(accepted), err)
	}
	if accepted[0] != 0b1010_0000 { // bit0、bit2（MSB-first）
		t.Fatalf("accepted 位图 = %08b, 期望 10100000", accepted[0])
	}
	persisted, err := ReceiverBitmapPersisted(id, 4)
	if err != nil || len(persisted) != 1 || persisted[0] != 0b1010_0000 {
		t.Fatalf("accept 后 persisted 位图 = %08b, 期望 10100000（write-through 应立即落盘）: err=%v",
			persisted, err)
	}
	// flush 在 write-through 下退化为整文件哈希收尾/全量回退时的兜底，幂等且不改变位图。
	if _, err := ReceiverFlush(id); err != nil {
		t.Fatalf("ReceiverFlush: %v", err)
	}
	persisted, err = ReceiverBitmapPersisted(id, 4)
	if err != nil || persisted[0] != 0b1010_0000 {
		t.Fatalf("flush 后 persisted 位图 = %08b, 期望 10100000", persisted[0])
	}
	if err := ReceiverSnapshotAck(id); err != nil {
		t.Fatalf("ReceiverSnapshotAck: %v", err)
	}
	// 重复 ack 幂等（dirty 已清，仍是 FC_OK）
	if err := ReceiverSnapshotAck(id); err != nil {
		t.Fatalf("重复 ReceiverSnapshotAck: %v", err)
	}

	// abandon 优先于 duplicate：index 1 的旧尝试被放弃
	if err := ReceiverAbandon(id, 1, "old-attempt"); err != nil {
		t.Fatalf("ReceiverAbandon: %v", err)
	}
	if _, err := ReceiverAccept(id, 1, chunks[1], "old-attempt"); !errors.Is(err, ErrReceiverAbandoned) {
		t.Fatalf("abandoned 尝试应返回 ErrReceiverAbandoned, got %v", err)
	}
	// 新尝试不受影响
	if _, err := ReceiverAccept(id, 1, chunks[1], "new-attempt"); err != nil {
		t.Fatalf("新尝试 accept 失败: %v", err)
	}
	if _, err := ReceiverAccept(id, 3, chunks[3], ""); err != nil {
		t.Fatalf("末片 accept 失败: %v", err)
	}
	if _, err := ReceiverFlush(id); err != nil {
		t.Fatalf("末次 ReceiverFlush: %v", err)
	}

	fileHash, bad, err := ReceiverFinalize(id, leaves, root)
	if err != nil || bad != -1 {
		t.Fatalf("ReceiverFinalize: bad=%d err=%v", bad, err)
	}
	if !bytes.Equal(fileHash, wantHash) {
		t.Fatalf("整文件哈希不一致: got=%x want=%x", fileHash, wantHash)
	}
	// finalize 成功：receiver 已移除
	if ReceiverExists(id) {
		t.Fatal("finalize 成功后 receiver 应已移除")
	}
	if _, _, err := ReceiverFinalize(id, leaves, root); !errors.Is(err, ErrReceiverNotFound) {
		t.Fatalf("缺 receiver 的 finalize 应返回 ErrReceiverNotFound, got %v", err)
	}

	// 落盘内容正确
	dst := filepath.Join(dir, "final.bin")
	if err := Move(tmp, dst); err != nil {
		t.Fatalf("Move: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("落盘内容不一致: err=%v", err)
	}
}

// TestReceiverBadChunkFinalizeAndReupload 坏块：finalize 返回 LEAF_MISMATCH + bad_index，
// 清该片位并保留 receiver；补传正确片后可再次 finalize 成功。
func TestReceiverBadChunkFinalizeAndReupload(t *testing.T) {
	const chunkSize = 512
	const total = 2 * chunkSize // 2 片
	data := make([]byte, total)
	for i := range data {
		data[i] = byte(i*13 + 1)
	}
	leaves, root, chunks := chunkedLeaves(t, data, chunkSize)

	dir := t.TempDir()
	tmp := filepath.Join(dir, "bad.part")
	if err := Preallocate(tmp, uint64(total)); err != nil {
		t.Fatalf("Preallocate: %v", err)
	}
	id := uniqueReceiverID(t)
	if created, err := ReceiverOpen(id, tmp, uint64(total), chunkSize, 2, nil, time.Hour); err != nil || !created {
		t.Fatalf("ReceiverOpen: created=%v err=%v", created, err)
	}
	defer func() { _ = ReceiverClose(id) }()

	if _, err := ReceiverAccept(id, 0, chunks[0], ""); err != nil {
		t.Fatalf("accept 0: %v", err)
	}
	bad := append([]byte(nil), chunks[1]...)
	bad[0] ^= 0xFF
	if _, err := ReceiverAccept(id, 1, bad, ""); err != nil {
		t.Fatalf("accept 坏片: %v", err)
	}
	if _, err := ReceiverFlush(id); err != nil {
		t.Fatalf("flush: %v", err)
	}

	_, badIndex, err := ReceiverFinalize(id, leaves, root)
	if !errors.Is(err, ErrLeafMismatch) || badIndex != 1 {
		t.Fatalf("期望 LEAF_MISMATCH bad_index=1, got bad=%d err=%v", badIndex, err)
	}
	if !ReceiverExists(id) {
		t.Fatal("坏块 finalize 失败后 receiver 应保留")
	}
	accepted, err := ReceiverBitmapAccepted(id, 2)
	if err != nil || accepted[0]&0b0100_0000 != 0 {
		t.Fatalf("坏块位应被清零: %08b err=%v", accepted[0], err)
	}
	// 补传正确片 → finalize 成功
	if _, err := ReceiverAccept(id, 1, chunks[1], ""); err != nil {
		t.Fatalf("补传 accept: %v", err)
	}
	fileHash, badIndex, err := ReceiverFinalize(id, leaves, root)
	if err != nil || badIndex != -1 || len(fileHash) != HashSize {
		t.Fatalf("补传后 finalize: bad=%d err=%v", badIndex, err)
	}
	if ReceiverExists(id) {
		t.Fatal("finalize 成功后 receiver 应已移除")
	}
}

// TestReceiverCloseIdempotent close 幂等，且关闭后 accept 返回 NOT_FOUND。
func TestReceiverCloseIdempotent(t *testing.T) {
	id := uniqueReceiverID(t)
	if err := ReceiverClose(id); err != nil {
		t.Fatalf("close 不存在的 receiver 应返回 nil, got %v", err)
	}
	dir := t.TempDir()
	tmp := filepath.Join(dir, "close.part")
	if err := Preallocate(tmp, 64); err != nil {
		t.Fatalf("Preallocate: %v", err)
	}
	if _, err := ReceiverOpen(id, tmp, 64, 64, 1, nil, time.Hour); err != nil {
		t.Fatalf("ReceiverOpen: %v", err)
	}
	if err := ReceiverClose(id); err != nil {
		t.Fatalf("close: %v", err)
	}
	if ReceiverExists(id) {
		t.Fatal("close 后 ReceiverExists 应为 false")
	}
	if _, err := ReceiverAccept(id, 0, make([]byte, 64), ""); !errors.Is(err, ErrReceiverNotFound) {
		t.Fatalf("close 后 accept 应返回 ErrReceiverNotFound, got %v", err)
	}
}
