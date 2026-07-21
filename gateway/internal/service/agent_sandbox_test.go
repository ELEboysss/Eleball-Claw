package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileSandbox_ResolvePath(t *testing.T) {
	base := t.TempDir()
	kb := filepath.Join(t.TempDir(), "kb")
	fs := NewFileSandbox(base, kb)

	// 正常 conversation 路径
	p, err := fs.ResolvePath("u1", "c1", "test.txt")
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p))

	// 路径逃逸应被拒绝
	_, err = fs.ResolvePath("u1", "c1", "../secret.txt")
	assert.Error(t, err)

	// 知识库路径
	p2, err := fs.ResolvePath("u1", "c1", "kb-doc.md")
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p2))
}

func TestFileSandbox_ReadWrite(t *testing.T) {
	base := t.TempDir()
	fs := NewFileSandbox(base, "")

	dir, err := fs.SessionDir("u1", "s1")
	require.NoError(t, err)

	path := filepath.Join(dir, "hello.txt")
	require.NoError(t, fs.WriteFile(path, []byte("hello")))

	data, err := fs.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	// 越界读取应被拒绝
	_, err = fs.ReadFile(filepath.Join(t.TempDir(), "outside.txt"))
	assert.Error(t, err)
}

func TestFileSandbox_RemoveSessionDir(t *testing.T) {
	base := t.TempDir()
	fs := NewFileSandbox(base, "")

	dir, err := fs.SessionDir("u1", "s1")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0644))

	require.NoError(t, fs.RemoveSessionDir(dir))
	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err))

	// 越界删除应被拒绝
	err = fs.RemoveSessionDir(t.TempDir())
	assert.Error(t, err)
}
