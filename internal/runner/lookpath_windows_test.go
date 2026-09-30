package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookPathAddsTheExtensionsOfPathext(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "deploy.cmd")
	if err := os.WriteFile(want, []byte("@echo off\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := lookPath("deploy", []string{"PATH=" + dir, "PATHEXT=.EXE;.CMD"}); got != want || err != nil {
		t.Errorf("lookPath = %q, %v; want %q", got, err, want)
	}
}

func TestLookPathTriesTheExtensionsOfPathextInOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"deploy.cmd", "deploy.exe"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := lookPath("deploy", []string{"PATH=" + dir, "PATHEXT=.EXE;.CMD"}); got != filepath.Join(dir, "deploy.exe") || err != nil {
		t.Errorf("lookPath = %q, %v; want deploy.exe", got, err)
	}
}

func TestLookPathTakesAProgramGivenWithItsExtension(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "deploy.ps1")
	if err := os.WriteFile(want, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := lookPath("deploy.ps1", []string{"PATH=" + dir, "PATHEXT=.EXE;.CMD"}); got != want || err != nil {
		t.Errorf("lookPath = %q, %v; want %q", got, err, want)
	}
}

func TestLookPathUsesTheUsualExtensionsWithoutPathext(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "deploy.bat")
	if err := os.WriteFile(want, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := lookPath("deploy", []string{"PATH=" + dir}); got != want || err != nil {
		t.Errorf("lookPath = %q, %v; want %q", got, err, want)
	}
}

func TestEnvValueComparesNamesWithoutCaseOnWindows(t *testing.T) {
	if got := envValue([]string{`Path=C:\Windows`}, "PATH"); got != `C:\Windows` {
		t.Errorf("envValue = %q, want C:\\Windows", got)
	}
}
