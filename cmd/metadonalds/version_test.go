package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNativeVersionFlag(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })
	t.Setenv("METADONALDS_CONFIG", "/missing/config.toml")
	for _, tc := range []struct{ name, injected, want string }{
		{"fallback", "", "dev"},
		{"explicit-dev", "dev", "dev"},
		{"release", "v1.2.3", "v1.2.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version = tc.injected
			cmd := newCommand()
			cmd.SetArgs([]string{"--version"})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if output.String() != "metadonalds "+tc.want+"\n" {
				t.Fatalf("version output %q", output.String())
			}
		})
	}
	cmd := newCommand()
	cmd.SetArgs([]string{"--help"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "--version") {
		t.Fatal("native version flag missing from help")
	}
}
