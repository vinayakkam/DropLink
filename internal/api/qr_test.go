package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoomQR(t *testing.T) {
	q, err := RoomQR("ABC234")
	if err != nil {
		t.Fatal(err)
	}
	if q.Text != "droplink://join/ABC234" || q.Size < 21 || len(q.Bits) != q.Size*q.Size {
		t.Fatalf("qr %d x %d, %d bits, %q", q.Size, q.Size, len(q.Bits), q.Text)
	}
	if strings.Trim(q.Bits, "01") != "" {
		t.Fatal("bits must be 0 and 1 only")
	}
	// Every QR code has a 7x7 finder pattern in the top-left corner:
	// a dark ring, a light ring and a dark 3x3 core.
	at := func(x, y int) byte { return q.Bits[y*q.Size+x] }
	for i := 0; i < 7; i++ {
		for _, p := range [][2]int{{i, 0}, {i, 6}, {0, i}, {6, i}} {
			if at(p[0], p[1]) != '1' {
				t.Fatalf("finder ring not dark at %v", p)
			}
		}
	}
	for x := 2; x <= 4; x++ {
		for y := 2; y <= 4; y++ {
			if at(x, y) != '1' {
				t.Fatalf("finder core not dark at %d,%d", x, y)
			}
		}
	}
	if at(1, 1) != '0' {
		t.Fatal("finder light ring is dark")
	}
	again, _ := RoomQR("ABC234")
	if again.Bits != q.Bits {
		t.Fatal("QR not deterministic")
	}
}

func TestQRRoute(t *testing.T) {
	e := newEnv(t)
	code, body := e.do(t, "GET", "/api/rooms/abc234/qr", "")
	var q QR
	if code != 200 || json.Unmarshal([]byte(body), &q) != nil || q.Text != "droplink://join/ABC234" {
		t.Fatalf("%d %s", code, body)
	}
	for _, bad := range []string{"nope", "ABC2O4", "ABCDEFG"} {
		if code, _ := e.do(t, "GET", "/api/rooms/"+bad+"/qr", ""); code != 400 {
			t.Errorf("%s: %d", bad, code)
		}
	}
	_, info := e.do(t, "GET", "/api/info", "")
	if !strings.Contains(info, `"qr"`) {
		t.Fatalf("qr feature not listed: %s", info)
	}
}
