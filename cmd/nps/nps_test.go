package main

import (
	"os"
	"reflect"
	"testing"
)

func TestNormalizeLegacyLongFlagsConvertsKnownSingleDashFlags(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	os.Args = []string{
		"nps",
		"-conf_path=test.conf",
		"-get2fa=ABCDEF",
		"-v",
	}

	normalizeLegacyLongFlags()

	want := []string{
		"nps",
		"--conf_path=test.conf",
		"--get2fa=ABCDEF",
		"-v",
	}
	if !reflect.DeepEqual(os.Args, want) {
		t.Fatalf("normalizeLegacyLongFlags() args = %#v, want %#v", os.Args, want)
	}
}

func TestNormalizeLegacyLongFlagsSupportsHyphenatedAliases(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	os.Args = []string{
		"nps",
		"-conf-path=test.conf",
		"-get2fa=ABCDEF",
	}

	normalizeLegacyLongFlags()

	want := []string{
		"nps",
		"--conf-path=test.conf",
		"--get2fa=ABCDEF",
	}
	if !reflect.DeepEqual(os.Args, want) {
		t.Fatalf("normalizeLegacyLongFlags() hyphenated args = %#v, want %#v", os.Args, want)
	}
}

func TestNormalizeLegacyLongFlagsLeavesUnknownAndDoubleDashUntouched(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	os.Args = []string{
		"nps",
		"-unknown=value",
		"--version",
		"install",
	}

	normalizeLegacyLongFlags()

	want := []string{
		"nps",
		"-unknown=value",
		"--version",
		"install",
	}
	if !reflect.DeepEqual(os.Args, want) {
		t.Fatalf("normalizeLegacyLongFlags() untouched args = %#v, want %#v", os.Args, want)
	}
}
