package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCommandParsing(t *testing.T) {
	t.Setenv("METADONALDS_CONFIG", "/missing/config.toml")
	for _, tc := range []struct {
		args  []string
		want  string
		fails bool
	}{
		{[]string{}, "Available Commands", false},
		{[]string{"serve", "--help"}, "--listen", false},
		{[]string{"tui", "--help"}, "--server", false},
		{[]string{"serve", "--help"}, "--open", false},
		{[]string{"desktop"}, "unknown command", true},
		{[]string{"web"}, "unknown command", true},
		{[]string{"serve", "--server", "http://localhost:8080"}, "unknown flag", true},
		{[]string{"unknown"}, "unknown command", true},
		{[]string{"serve", "extra"}, "unknown command", true},
		{[]string{"serve", "--listen="}, "--listen must not be empty", true},
		{[]string{"serve", "--config", "/flag/config.toml"}, "/flag/config.toml", true},
		{[]string{"serve"}, "/missing/config.toml", true},
		{[]string{"tui", "--server", "invalid"}, "server must be an HTTP(S) origin", true},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd := newCommand()
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if (err != nil) != tc.fails {
				t.Fatalf("error = %v", err)
			}
			if err != nil {
				output.WriteString(err.Error())
			}
			if !strings.Contains(output.String(), tc.want) {
				t.Fatalf("output %q does not contain %q", output.String(), tc.want)
			}
		})
	}
}
