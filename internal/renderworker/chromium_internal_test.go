package renderworker

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/stretchr/testify/require"
)

func TestSanitizeTarName(t *testing.T) {
	t.Parallel()

	if _, ok := sanitizeTarName(""); ok {
		t.Fatalf("expected empty to be rejected")
	}
	if _, ok := sanitizeTarName("."); ok {
		t.Fatalf("expected dot to be rejected")
	}

	name, ok := sanitizeTarName("/a/b/../c.txt")
	if !ok || name != "a/c.txt" {
		t.Fatalf("unexpected sanitize: ok=%v name=%q", ok, name)
	}
}

func TestValidateTarTarget_PreventsTraversal(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	if _, err := validateTarTarget(dest, "../evil.txt", "../evil.txt"); err == nil {
		t.Fatalf("expected traversal error")
	}
}

func TestExtractTar_ExtractsFilesAndRejectsTraversal(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()

	makeTar := func(entries func(tw *tar.Writer) error) []byte {
		t.Helper()
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := entries(tw); err != nil {
			t.Fatalf("write tar: %v", err)
		}
		if err := tw.Close(); err != nil {
			t.Fatalf("close tar: %v", err)
		}
		return buf.Bytes()
	}

	// Happy path: directory + file.
	archive := makeTar(func(tw *tar.Writer) error {
		if err := tw.WriteHeader(&tar.Header{Name: "ok", Typeflag: tar.TypeDir, Mode: 0o750}); err != nil {
			return err
		}
		data := []byte("hello")
		if err := tw.WriteHeader(&tar.Header{Name: "ok/file.txt", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
		// Symlink should be ignored.
		if err := tw.WriteHeader(&tar.Header{Name: "ok/link", Typeflag: tar.TypeSymlink}); err != nil {
			return err
		}
		return nil
	})

	tr := tar.NewReader(bytes.NewReader(archive))
	if err := extractTar(tr, dest, tarExtractLimits{MaxFileBytes: 1024, MaxTotalBytes: 1024}); err != nil {
		t.Fatalf("extractTar err: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "ok", "file.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("unexpected extracted content: %q", string(b))
	}

	// Traversal attempt should error.
	badArchive := makeTar(func(tw *tar.Writer) error {
		data := []byte("x")
		if err := tw.WriteHeader(&tar.Header{Name: "../evil.txt", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	})
	tr2 := tar.NewReader(bytes.NewReader(badArchive))
	if err := extractTar(tr2, dest, tarExtractLimits{MaxFileBytes: 1024, MaxTotalBytes: 1024}); err == nil {
		t.Fatalf("expected traversal error")
	}
}

func TestInflateBrotliFile_ErrorsAndSuccess(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := inflateBrotliFile("", dest, 0o600); err == nil {
		t.Fatalf("expected error for empty src")
	}

	// If dest exists, inflation is skipped.
	if err := os.WriteFile(dest, []byte("already"), 0o600); err != nil {
		t.Fatalf("write dest: %v", err)
	}
	if err := inflateBrotliFile("missing", dest, 0o600); err != nil {
		t.Fatalf("expected skip when dest exists, got %v", err)
	}

	// Successful inflate.
	src := filepath.Join(t.TempDir(), "in.br")
	{
		var buf bytes.Buffer
		w := brotli.NewWriter(&buf)
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Fatalf("brotli write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("brotli close: %v", err)
		}
		if err := os.WriteFile(src, buf.Bytes(), 0o600); err != nil {
			t.Fatalf("write src: %v", err)
		}
	}

	dest2 := filepath.Join(t.TempDir(), "inflated.txt")
	if err := inflateBrotliFile(src, dest2, 0o600); err != nil {
		t.Fatalf("inflateBrotliFile err: %v", err)
	}
	b, err := os.ReadFile(dest2)
	if err != nil {
		t.Fatalf("read inflated: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("unexpected inflated content: %q", string(b))
	}
}

func TestInflateTarBrotli_SuccessSkipAndOptional(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	src := filepath.Join(t.TempDir(), "fonts.tar.br")

	// Build tar -> brotli file.
	{
		var tarBuf bytes.Buffer
		tw := tar.NewWriter(&tarBuf)
		data := []byte("font")
		if err := tw.WriteHeader(&tar.Header{Name: "fonts/a.txt", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write: %v", err)
		}
		if err := tw.Close(); err != nil {
			t.Fatalf("tar close: %v", err)
		}

		var brBuf bytes.Buffer
		bw := brotli.NewWriter(&brBuf)
		if _, err := io.Copy(bw, bytes.NewReader(tarBuf.Bytes())); err != nil {
			t.Fatalf("brotli copy: %v", err)
		}
		if err := bw.Close(); err != nil {
			t.Fatalf("brotli close: %v", err)
		}
		if err := os.WriteFile(src, brBuf.Bytes(), 0o600); err != nil {
			t.Fatalf("write src: %v", err)
		}
	}

	// Extract into a non-existent directory.
	destDir := filepath.Join(dest, "fonts")
	if err := inflateTarBrotli(src, destDir, "fonts"); err != nil {
		t.Fatalf("inflateTarBrotli err: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(destDir, "fonts", "a.txt"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if strings.TrimSpace(string(b)) != "font" {
		t.Fatalf("unexpected extracted content: %q", string(b))
	}

	// Second call should skip (dest dir exists).
	if err := inflateTarBrotli(src, destDir, "fonts"); err != nil {
		t.Fatalf("expected skip to succeed, got %v", err)
	}

	// Optional swiftshader: missing file should be ignored.
	if err := inflateTarBrotli(filepath.Join(t.TempDir(), "missing.tar.br"), t.TempDir(), "swiftshader"); err != nil {
		t.Fatalf("expected optional swiftshader missing to be ok, got %v", err)
	}
}

func TestSetupChromiumEnv(t *testing.T) {
	t.Setenv("FONTCONFIG_PATH", "")
	t.Setenv("HOME", "")
	t.Setenv("LD_LIBRARY_PATH", "other")

	setupChromiumEnv()

	if got := os.Getenv("FONTCONFIG_PATH"); got == "" || !strings.Contains(got, "fonts") {
		t.Fatalf("expected FONTCONFIG_PATH set, got %q", got)
	}
	if got := os.Getenv("HOME"); got == "" {
		t.Fatalf("expected HOME set")
	}
	ld := os.Getenv("LD_LIBRARY_PATH")
	if !strings.Contains(ld, "al2023") {
		t.Fatalf("expected LD_LIBRARY_PATH to include al2023, got %q", ld)
	}
}

func TestEnsureChromiumReady_InflatesAllAssets(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	binDir := t.TempDir()
	t.Setenv("CHROMIUM_BIN_DIR", binDir)

	writeBrotli := func(path string, payload []byte) {
		t.Helper()
		var buf bytes.Buffer
		w := brotli.NewWriter(&buf)
		_, err := w.Write(payload)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	}

	writeTarBrotli := func(path string, files map[string]string) {
		t.Helper()
		var tarBuf bytes.Buffer
		tw := tar.NewWriter(&tarBuf)
		for name, content := range files {
			body := []byte(content)
			require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(body))}))
			_, err := tw.Write(body)
			require.NoError(t, err)
		}
		require.NoError(t, tw.Close())

		writeBrotli(path, tarBuf.Bytes())
	}

	writeBrotli(filepath.Join(binDir, "chromium.br"), []byte("chromium-binary"))
	writeTarBrotli(filepath.Join(binDir, "fonts.tar.br"), map[string]string{"fonts/a.txt": "font"})
	writeTarBrotli(filepath.Join(binDir, "swiftshader.tar.br"), map[string]string{"libGLESv2.so": "shader"})
	writeTarBrotli(filepath.Join(binDir, "al2023.tar.br"), map[string]string{"lib/libfoo.so": "lib"})

	execPath, err := ensureChromiumReady(context.Background())
	require.NoError(t, err)
	wantExec := filepath.Join(tmp, "chromium")
	require.Equal(t, wantExec, execPath)

	b, err := os.ReadFile(execPath)
	require.NoError(t, err)
	require.Equal(t, "chromium-binary", string(b))

	_, statErr := os.Stat(filepath.Join(tmp, "fonts"))
	require.NoError(t, statErr)
	_, statErr = os.Stat(filepath.Join(tmp, "al2023", "lib"))
	require.NoError(t, statErr)

	// Second call should be a memoized hit.
	execPath2, err := ensureChromiumReady(context.Background())
	require.NoError(t, err)
	require.Equal(t, execPath, execPath2)
}

func TestInitChromium_SkipsInflationWhenBinaryAlreadyPresent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("FONTCONFIG_PATH", "")
	t.Setenv("HOME", "")
	t.Setenv("LD_LIBRARY_PATH", "")

	execPath := filepath.Join(tmp, "chromium")
	if err := os.WriteFile(execPath, []byte("already"), 0o600); err != nil {
		t.Fatalf("write chromium: %v", err)
	}

	got, err := initChromium()
	if err != nil {
		t.Fatalf("initChromium err: %v", err)
	}
	if got != execPath {
		t.Fatalf("expected exec path %q, got %q", execPath, got)
	}
	if os.Getenv("FONTCONFIG_PATH") == "" || os.Getenv("HOME") == "" || os.Getenv("LD_LIBRARY_PATH") == "" {
		t.Fatalf("expected chromium env set")
	}
}

func TestInflateBrotliFile_ReturnsErrorWhenSourceMissing(t *testing.T) {
	t.Parallel()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := inflateBrotliFile(filepath.Join(t.TempDir(), "missing.br"), dest, 0o600); err == nil {
		t.Fatalf("expected error for missing src")
	}
}

func TestInflateBrotliFile_ReturnsErrorWhenDestIsDirectory(t *testing.T) {
	t.Parallel()

	// Create a valid brotli file.
	src := filepath.Join(t.TempDir(), "in.br")
	{
		var buf bytes.Buffer
		w := brotli.NewWriter(&buf)
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Fatalf("brotli write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("brotli close: %v", err)
		}
		if err := os.WriteFile(src, buf.Bytes(), 0o600); err != nil {
			t.Fatalf("write src: %v", err)
		}
	}

	destDir := filepath.Join(t.TempDir(), "outdir")
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}

	if err := inflateBrotliFile(src, destDir, 0o600); err == nil {
		t.Fatalf("expected error when dest is directory")
	}
}

func TestInflateTarBrotli_ReturnsErrorWhenSourceMissingAndNotOptional(t *testing.T) {
	t.Parallel()

	destDir := filepath.Join(t.TempDir(), "fonts") // must not exist to avoid skip
	if err := inflateTarBrotli(filepath.Join(t.TempDir(), "missing.tar.br"), destDir, "fonts"); err == nil {
		t.Fatalf("expected error for missing fonts archive")
	}
}

func TestExtractTarFile_ReturnsErrorsForLimitsAndShortReads(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()

	// Header too large.
	_, err := extractTarFile(nil, &tar.Header{Name: "big.txt", Typeflag: tar.TypeReg, Size: 5}, filepath.Join(dest, "big.txt"), 0, tarExtractLimits{MaxFileBytes: 4})
	if err == nil {
		t.Fatalf("expected size limit error")
	}

	// Total too large.
	_, err = extractTarFile(nil, &tar.Header{Name: "big.txt", Typeflag: tar.TypeReg, Size: 5}, filepath.Join(dest, "big2.txt"), 0, tarExtractLimits{MaxTotalBytes: 4})
	if err == nil {
		t.Fatalf("expected total limit error")
	}

	// Short read when tar reader has no current file.
	tr := tar.NewReader(bytes.NewReader(nil))
	_, err = extractTarFile(tr, &tar.Header{Name: "x.txt", Typeflag: tar.TypeReg, Size: 1}, filepath.Join(dest, "x.txt"), 0, tarExtractLimits{MaxFileBytes: 10, MaxTotalBytes: 10})
	if err == nil {
		t.Fatalf("expected short read error")
	}
}
