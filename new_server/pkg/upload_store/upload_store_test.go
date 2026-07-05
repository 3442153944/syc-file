package upload_store

import (
	"reflect"
	"testing"
)

// TestMissingChunks 校验位序与 Redis SETBIT 一致（MSB-first）。
func TestMissingChunks(t *testing.T) {
	cases := []struct {
		name       string
		bitmap     []byte
		chunkCount int
		want       []int
	}{
		{"nil位图全缺", nil, 3, []int{0, 1, 2}},
		{"首片已收", []byte{0x80}, 8, []int{1, 2, 3, 4, 5, 6, 7}}, // offset0 = 最高位
		{"全收", []byte{0xFF}, 8, []int{}},
		{"隔位收", []byte{0xA0}, 4, []int{1, 3}}, // 1010 0000 → 收到 0、2
		{"跨字节", []byte{0x00, 0x80}, 9, []int{0, 1, 2, 3, 4, 5, 6, 7}}, // 第8片(byte1最高位)已收
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MissingChunks(tc.bitmap, tc.chunkCount)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("MissingChunks = %v, 期望 %v", got, tc.want)
			}
		})
	}
}

// TestSessionIDDeterministic 相同身份必得相同 id，文件哈希变则 id 变。
func TestSessionIDDeterministic(t *testing.T) {
	a := SessionID(1, "E:\\a\\b.bin", "abcd", 100, 10)
	b := SessionID(1, "E:\\a\\b.bin", "abcd", 100, 10)
	if a != b {
		t.Fatalf("同身份 id 不一致: %s vs %s", a, b)
	}
	c := SessionID(1, "E:\\a\\b.bin", "ef01", 100, 10) // file_hash 变
	if a == c {
		t.Fatalf("文件哈希变化后 id 应不同")
	}
}
