package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSubmitViaRC(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/backend/command" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("wrong RC request: %s %s", r.Method, r.URL)
		}
		var input struct {
			Command string   `json:"command"`
			FS      string   `json:"fs"`
			Arg     []string `json:"arg"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.Command != "addurl" || input.FS != "pikpak:My Pack" || len(input.Arg) != 1 || input.Arg[0] != "magnet:?xt=urn:btih:"+testHash {
			t.Errorf("wrong RC arguments: %#v", input)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"result":{}}`))}, nil
	})}
	if err := submitViaRC(context.Background(), client, "pikpak:My Pack", testHash); err != nil {
		t.Fatal(err)
	}
	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(`{"error":"failed"}`))}, nil
	})
	if err := submitViaRC(context.Background(), client, "pikpak:My Pack", testHash); err == nil {
		t.Fatal("RC failure reported as success")
	}
}
