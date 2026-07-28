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

// TestFileSandbox_ResolveProjectPath AR-06：cwd 路径解析与逃逸防护
func TestFileSandbox_ResolveProjectPath(t *testing.T) {
	cwd := t.TempDir()
	fs := NewFileSandbox(t.TempDir(), t.TempDir())

	// 正常相对路径
	p, err := fs.ResolveProjectPath(cwd, "src/main.go")
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(p))
	assert.Contains(t, p, "src")

	// .. 逃逸应被拒绝
	_, err = fs.ResolveProjectPath(cwd, "../secret.txt")
	assert.Error(t, err)

	// 空路径返回 cwd 自身
	p2, err := fs.ResolveProjectPath(cwd, "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(cwd), p2)

	// 软链逃逸防护：cwd 内建软链指向外部，解析后应被拒
	target := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(target, []byte("secret"), 0644))
	link := filepath.Join(cwd, "link.txt")
	if err := os.Symlink(target, link); err == nil {
		// 软链创建成功（非 Windows 或有权限）：解析应被拒
		_, err := fs.ResolveProjectPath(cwd, "link.txt")
		assert.Error(t, err, "软链指向 cwd 外应被拒绝")
	}
}

// TestFileSandbox_ListDir AR-06：目录列举
func TestFileSandbox_ListDir(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("a"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "sub"), 0750))
	fs := NewFileSandbox(t.TempDir(), t.TempDir())

	entries, err := fs.ListDir(cwd, "")
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	// 越界列举应被拒
	_, err = fs.ListDir(cwd, "../")
	assert.Error(t, err)
}

// TestFileSandbox_WithProjectRoot AR-06：cwd 第三根放行读写
func TestFileSandbox_WithProjectRoot(t *testing.T) {
	cwd := t.TempDir()
	target := filepath.Join(cwd, "proj.txt")
	require.NoError(t, os.WriteFile(target, []byte("data"), 0640))
	base := t.TempDir()
	fs := NewFileSandbox(base, "").WithProjectRoot(cwd)

	// projectRoot 下可读
	data, err := fs.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "data", string(data))

	// projectRoot 下可写
	newFile := filepath.Join(cwd, "new.txt")
	require.NoError(t, fs.WriteFile(newFile, []byte("written")))
	got, _ := os.ReadFile(newFile)
	assert.Equal(t, "written", string(got))

	// basePath 外、projectRoot 外仍被拒
	outside := filepath.Join(t.TempDir(), "evil.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0640))
	_, err = fs.ReadFile(outside)
	assert.Error(t, err)
}

// TestFileSandbox_DirMgmt_InCwd AR-21：cwd 文件管理（建/移/删）与逃逸防护
func TestFileSandbox_DirMgmt_InCwd(t *testing.T) {
	cwd := t.TempDir()
	fs := NewFileSandbox(t.TempDir(), t.TempDir())

	// MkdirInCwd 递归建目录
	abs, err := fs.MkdirInCwd(cwd, "sub/deep")
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(abs))
	info, err := os.Stat(abs)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// MkdirInCwd .. 越界拒绝
	_, err = fs.MkdirInCwd(cwd, "../evil")
	assert.Error(t, err)

	// MoveInCwd 重命名：先建文件再 move
	src := filepath.Join(cwd, "a.txt")
	require.NoError(t, os.WriteFile(src, []byte("data"), 0640))
	dstAbs, err := fs.MoveInCwd(cwd, "a.txt", "b.txt")
	require.NoError(t, err)
	assert.Contains(t, dstAbs, "b.txt")
	_, err = os.Stat(src)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(cwd, "b.txt"))
	require.NoError(t, err)

	// MoveInCwd dst 越界拒绝
	_, err = fs.MoveInCwd(cwd, "b.txt", "../escape.txt")
	assert.Error(t, err)

	// RemoveAllInCwd 删文件
	_, err = fs.RemoveAllInCwd(cwd, "b.txt")
	require.NoError(t, err)

	// RemoveAllInCwd 递归删目录
	_, err = fs.RemoveAllInCwd(cwd, "sub")
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(cwd, "sub"))
	assert.True(t, os.IsNotExist(err))

	// RemoveAllInCwd 拒删根（"." / ""）
	_, err = fs.RemoveAllInCwd(cwd, ".")
	assert.Error(t, err)
	_, err = fs.RemoveAllInCwd(cwd, "")
	assert.Error(t, err)

	// RemoveAllInCwd .. 越界拒绝
	_, err = fs.RemoveAllInCwd(cwd, "../outside")
	assert.Error(t, err)
}
