package devbuild

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func member(name string, data []byte) []byte {
	b := []byte(fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name, 0, 0, 0, 0644, len(data)))
	b = append(b, data...)
	if len(data)%2 != 0 {
		b = append(b, '\n')
	}
	return b
}

func TestArchiveTarget(t *testing.T) {
	elf := make([]byte, 20)
	copy(elf, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	binary.LittleEndian.PutUint16(elf[18:], 62)
	arm := append([]byte(nil), elf...)
	binary.LittleEndian.PutUint16(arm[18:], 183)
	coff := make([]byte, 20)
	binary.LittleEndian.PutUint16(coff, 0x8664)
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"linux-amd64", member("/0", elf), "linux/amd64"},
		{"linux-arm64", member("core.o/", arm), "linux/arm64"},
		{"windows-amd64", member("core.o/", coff), "windows/amd64"},
		{"symbol-and-name-tables", append(append(member("/", []byte("index")), member("//", []byte("long-name.o/\n"))...), member("/0", elf)...), "linux/amd64"},
		{"bsd-name", member("#1/6", append([]byte("core.o"), elf...)), "linux/amd64"},
		{"unknown-object", member("core.o/", []byte("not an object")), ""},
		{"empty-archive", nil, ""},
		{"truncated-member", []byte("short"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "libsync_core.a")
			if err := os.WriteFile(path, append([]byte("!<arch>\n"), tc.data...), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := ArchiveTarget(path)
			if tc.want == "" {
				if err == nil {
					t.Fatal("expected rejection")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestArchiveMissingOrInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.a")
	if _, err := ArchiveTarget(path); !os.IsNotExist(err) {
		t.Fatalf("missing archive: %v", err)
	}
	if err := os.WriteFile(path, []byte("not an archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ArchiveTarget(path); err == nil {
		t.Fatal("invalid magic accepted")
	}
}

func TestEnsureCore(t *testing.T) {
	elf := make([]byte, 20)
	copy(elf, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	binary.LittleEndian.PutUint16(elf[18:], 62)
	coff := make([]byte, 20)
	binary.LittleEndian.PutUint16(coff, 0x8664)
	for _, tc := range []struct {
		name               string
		initial, rebuilt   []byte
		buildErr           bool
		wantBuild, wantErr bool
	}{
		{"matching-reused", coff, nil, false, false, false},
		{"missing-rebuilt", nil, coff, false, true, false},
		{"foreign-rebuilt", elf, coff, false, true, false},
		{"invalid-rebuilt", []byte("broken"), coff, false, true, false},
		{"build-failure-stops", elf, nil, true, true, true},
		{"wrong-rebuild-rejected", elf, elf, false, true, true},
		{"missing-rebuild-rejected", nil, nil, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			libdir := filepath.Join(root, "sync_core", "lib")
			if err := os.MkdirAll(libdir, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(b []byte) error {
				return os.WriteFile(filepath.Join(libdir, "libsync_core.a"), append([]byte("!<arch>\n"), member("core.o/", b)...), 0600)
			}
			if tc.initial != nil {
				if err := write(tc.initial); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			err := ensureCore(root, "windows/amd64", func() error {
				called = true
				if tc.buildErr {
					return errors.New("compiler failed")
				}
				if tc.rebuilt != nil {
					return write(tc.rebuilt)
				}
				return nil
			})
			if called != tc.wantBuild || (err != nil) != tc.wantErr {
				t.Fatalf("build=%v err=%v; want build=%v error=%v", called, err, tc.wantBuild, tc.wantErr)
			}
		})
	}
}
