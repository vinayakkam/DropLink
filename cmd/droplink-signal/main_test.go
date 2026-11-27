package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHubStartsServesAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, w := io.Pipe()
	exit := make(chan int, 1)
	go func() { exit <- run(ctx, []string{"-addr", "127.0.0.1:0"}, w, io.Discard); w.Close() }()

	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimSpace(strings.TrimPrefix(line, "droplink-signal listening on "))
	go func() { _, _ = io.Copy(io.Discard, out) }()

	res, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	http.DefaultClient.CloseIdleConnections()
	if res.StatusCode != 200 || !strings.Contains(string(b), `"rooms":0`) {
		t.Fatalf("healthz %d %s", res.StatusCode, b)
	}

	cancel()
	select {
	case code := <-exit:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hub did not stop")
	}
}

func TestBadFlags(t *testing.T) {
	if code := run(context.Background(), []string{"-nope"}, io.Discard, io.Discard); code != 2 {
		t.Fatalf("exit %d", code)
	}
}
