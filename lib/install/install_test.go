package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/djylb/nps/lib/common"
)

func TestPathExists(t *testing.T) {
	tmpDir := t.TempDir()
	existing := filepath.Join(tmpDir, "exists.txt")
	if err := os.WriteFile(existing, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if ok, err := pathExists(existing); err != nil || !ok {
		t.Fatalf("pathExists(existing) = (%v, %v), want (true, nil)", ok, err)
	}

	missing := filepath.Join(tmpDir, "missing.txt")
	if ok, err := pathExists(missing); err != nil || ok {
		t.Fatalf("pathExists(missing) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.txt")
	content := []byte("nps-copy-file")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("WriteFile(src) error = %v", err)
	}

	dest := filepath.Join(tmpDir, "nested", "dest.txt")
	n, err := copyFile(src, dest)
	if err != nil {
		t.Fatalf("copyFile() error = %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("copyFile() copied = %d, want %d", n, len(content))
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile(dest) error = %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("dest content = %q, want %q", got, content)
	}
}

func TestCopyFileSamePathNoop(t *testing.T) {
	tmpDir := t.TempDir()
	p := filepath.Join(tmpDir, "same.txt")
	if err := os.WriteFile(p, []byte("same"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	n, err := copyFile(p, p)
	if err != nil {
		t.Fatalf("copyFile(same path) error = %v", err)
	}
	if n != 0 {
		t.Fatalf("copyFile(same path) copied = %d, want 0", n)
	}
}

func TestCopyFileReturnsErrorWhenDestinationParentInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(src) error = %v", err)
	}

	blocker := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blocker, []byte("not-a-dir"), 0o644); err != nil {
		t.Fatalf("WriteFile(blocker) error = %v", err)
	}

	if _, err := copyFile(src, filepath.Join(blocker, "dest.txt")); err == nil {
		t.Fatal("copyFile() error = nil, want invalid parent error")
	}
}

func TestCopyDir(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(filepath.Join(src, "inner"), 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "inner", "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatalf("WriteFile(a.txt) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "b.txt"), []byte("B"), 0o644); err != nil {
		t.Fatalf("WriteFile(b.txt) error = %v", err)
	}

	dest := filepath.Join(tmpDir, "dest")
	if err := CopyDir(src, dest); err != nil {
		t.Fatalf("CopyDir() error = %v", err)
	}

	for rel, want := range map[string]string{
		"inner/a.txt": "A",
		"b.txt":       "B",
	} {
		got, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("dest %s = %q, want %q", rel, got, want)
		}
	}
}

func TestCopyDirValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()
	notDir := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	destDir := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(destDir) error = %v", err)
	}

	if err := CopyDir(notDir, destDir); err == nil {
		t.Fatal("CopyDir(non-directory src) error = nil, want error")
	}

	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(srcDir) error = %v", err)
	}
	if err := CopyDir(srcDir, notDir); err == nil {
		t.Fatal("CopyDir(file destination) error = nil, want error")
	}
}

func TestCopyDirPropagatesNestedCopyError(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(filepath.Join(src, "inner"), 0o755); err != nil {
		t.Fatalf("MkdirAll(src/inner) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "inner", "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatalf("WriteFile(a.txt) error = %v", err)
	}

	dest := filepath.Join(tmpDir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("MkdirAll(dest) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "inner"), []byte("not-a-directory"), 0o644); err != nil {
		t.Fatalf("WriteFile(dest/inner) error = %v", err)
	}

	if err := CopyDir(src, dest); err == nil {
		t.Fatal("CopyDir() error = nil, want nested copy failure")
	}
}

func TestBuildDownloadTLSConfig(t *testing.T) {
	cfg := buildDownloadTLSConfig("github.com:443")
	if cfg.InsecureSkipVerify {
		t.Fatal("buildDownloadTLSConfig() should keep certificate verification enabled")
	}
	if cfg.ServerName != "github.com" {
		t.Fatalf("buildDownloadTLSConfig(hostname) ServerName = %q, want github.com", cfg.ServerName)
	}

	ipCfg := buildDownloadTLSConfig("127.0.0.1:443")
	if ipCfg.InsecureSkipVerify {
		t.Fatal("buildDownloadTLSConfig(ip) should keep certificate verification enabled")
	}
	if ipCfg.ServerName != "" {
		t.Fatalf("buildDownloadTLSConfig(ip) ServerName = %q, want empty", ipCfg.ServerName)
	}

	rawHostCfg := buildDownloadTLSConfig("downloads.example.com")
	if rawHostCfg.ServerName != "downloads.example.com" {
		t.Fatalf("buildDownloadTLSConfig(raw hostname) ServerName = %q, want downloads.example.com", rawHostCfg.ServerName)
	}
}

func TestMkidrDirAllReturnsErrorForInvalidBase(t *testing.T) {
	tmpDir := t.TempDir()
	base := filepath.Join(tmpDir, "base-file")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(base) error = %v", err)
	}

	if err := MkidrDirAll(base, "child"); err == nil {
		t.Fatal("MkidrDirAll() error = nil, want invalid base path error")
	}
}

func preserveFile(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			t.Fatalf("preserveFile(%q): directories are not supported", path)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("ReadFile(%q) error = %v", path, readErr)
		}
		mode := info.Mode()
		t.Cleanup(func() {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
				_ = os.WriteFile(path, data, mode)
			}
		})
		return
	}
	if !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
	})
}

func stageAppFiles(t *testing.T, files map[string]string) string {
	t.Helper()

	appPath := common.GetAppPath()
	probe := filepath.Join(appPath, ".codex-install-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o644); err != nil {
		t.Skipf("app path %q is not writable: %v", appPath, err)
	}
	_ = os.Remove(probe)

	for rel, content := range files {
		path := filepath.Join(appPath, filepath.FromSlash(rel))
		preserveFile(t, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	return appPath
}

func TestNPS_WindowsInstallsStaticFilesIntoConfiguredPath(t *testing.T) {
	if !common.IsWindows() {
		t.Skip("this installation flow is Windows-specific in test mode")
	}

	oldConfPath := common.ConfPath
	installPath := filepath.Join(t.TempDir(), "install")
	common.ConfPath = installPath
	t.Cleanup(func() {
		common.ConfPath = oldConfPath
	})

	appPath := stageAppFiles(t, map[string]string{
		"nps.exe":              "nps-binary",
		"conf/nps.conf":        "[common]\nserver_addr=127.0.0.1:8024\nvkey=test\n",
		"web/views/index.html": "<html>view</html>",
		"web/static/app.js":    "console.log('static');",
	})
	preserveFile(t, filepath.Join(appPath, "nps-update.exe"))

	binPath, err := NPS()
	if err != nil {
		t.Fatalf("NPS() error = %v", err)
	}
	if binPath == "" {
		t.Fatal("NPS() returned empty binary path")
	}

	for rel, want := range map[string]string{
		"conf/nps.conf":         "[common]\nserver_addr=127.0.0.1:8024\nvkey=test\n",
		"conf/nps.conf.default": "[common]\nserver_addr=127.0.0.1:8024\nvkey=test\n",
		"web/views/index.html":  "<html>view</html>",
		"web/static/app.js":     "console.log('static');",
	} {
		got, err := os.ReadFile(filepath.Join(installPath, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}

	updateBinary, err := os.ReadFile(filepath.Join(appPath, "nps-update.exe"))
	if err != nil {
		t.Fatalf("ReadFile(nps-update.exe) error = %v", err)
	}
	if string(updateBinary) != "nps-binary" {
		t.Fatalf("nps-update.exe = %q, want copied nps binary", updateBinary)
	}
}

func TestNPC_WindowsCreatesInstallDirectoryAndUpdateBinary(t *testing.T) {
	if !common.IsWindows() {
		t.Skip("this installation flow is Windows-specific in test mode")
	}

	oldConfPath := common.ConfPath
	installPath := filepath.Join(t.TempDir(), "npc-install")
	common.ConfPath = installPath
	t.Cleanup(func() {
		common.ConfPath = oldConfPath
	})

	appPath := stageAppFiles(t, map[string]string{
		"npc.exe": "npc-binary",
	})
	preserveFile(t, filepath.Join(appPath, "npc-update.exe"))

	if err := NPC(); err != nil {
		t.Fatalf("NPC() error = %v", err)
	}

	if info, err := os.Stat(installPath); err != nil || !info.IsDir() {
		t.Fatalf("install path = (%v, %v), want created directory", info, err)
	}

	updateBinary, err := os.ReadFile(filepath.Join(appPath, "npc-update.exe"))
	if err != nil {
		t.Fatalf("ReadFile(npc-update.exe) error = %v", err)
	}
	if string(updateBinary) != "npc-binary" {
		t.Fatalf("npc-update.exe = %q, want copied npc binary", updateBinary)
	}
}
