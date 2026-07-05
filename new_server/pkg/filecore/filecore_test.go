package filecore

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestSmoke 走通完整链路：预分配 → 乱序写分片 → finalize 校验 → 落盘，
// 顺带验证 cgo 链接成功与哈希/Merkle 一致性。
func TestSmoke(t *testing.T) {
	if v := ABIVersion(); v != 1 {
		t.Fatalf("ABIVersion = %d, 期望 1（cgo 链接异常）", v)
	}

	const chunkSize = 4 << 20 // 4MiB
	// 造 10MiB 数据 → 3 片（4+4+2MiB），触发最后一片短块路径
	total := 10 << 20
	data := make([]byte, total)
	rng := rand.New(rand.NewSource(42))
	rng.Read(data)

	// 分片 + 计算叶子哈希
	var leaves []byte
	var offsets []uint64
	var chunks [][]byte
	for off := 0; off < total; off += chunkSize {
		end := off + chunkSize
		if end > total {
			end = total
		}
		chunk := data[off:end]
		leaf, err := HashChunk(chunk)
		if err != nil {
			t.Fatalf("HashChunk: %v", err)
		}
		leaves = append(leaves, leaf...)
		offsets = append(offsets, uint64(off))
		chunks = append(chunks, chunk)
	}

	root, err := MerkleRoot(leaves)
	if err != nil {
		t.Fatalf("MerkleRoot: %v", err)
	}

	dir := t.TempDir()
	tmp := filepath.Join(dir, "upload.tmp")
	if err := Preallocate(tmp, uint64(total)); err != nil {
		t.Fatalf("Preallocate: %v", err)
	}

	// 乱序写：2,0,1
	order := []int{2, 0, 1}
	for _, i := range order {
		leaf := leaves[i*HashSize : (i+1)*HashSize]
		if err := ChunkWrite(tmp, offsets[i], chunks[i], leaf); err != nil {
			t.Fatalf("ChunkWrite[%d]: %v", i, err)
		}
	}

	fileHash, bad, err := Finalize(tmp, chunkSize, uint64(total), leaves, root)
	if err != nil {
		t.Fatalf("Finalize: %v (badIndex=%d)", err, bad)
	}
	if bad != -1 {
		t.Fatalf("Finalize badIndex = %d, 期望 -1", bad)
	}
	if len(fileHash) != HashSize {
		t.Fatalf("fileHash 长度 = %d", len(fileHash))
	}

	// 落盘并比对内容
	dst := filepath.Join(dir, "final.bin")
	if err := Move(tmp, dst); err != nil {
		t.Fatalf("Move: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("落盘内容与原始不一致")
	}
}

// TestLeafMismatch 验证坏分片被早校验拒绝。
func TestLeafMismatch(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "x.tmp")
	if err := Preallocate(tmp, 1024); err != nil {
		t.Fatalf("Preallocate: %v", err)
	}
	data := bytes.Repeat([]byte{0xAB}, 1024)
	wrongLeaf := make([]byte, HashSize) // 全 0，必不匹配
	err := ChunkWrite(tmp, 0, data, wrongLeaf)
	if err != ErrLeafMismatch {
		t.Fatalf("期望 ErrLeafMismatch，得到 %v", err)
	}
}
