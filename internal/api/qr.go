package api

import (
	"net/http"
	"strings"

	"rsc.io/qr"

	"github.com/vinayakkam/droplink/internal/server"
	"github.com/vinayakkam/droplink/internal/signal"
)

// JoinURL is the deep link that opens DropLink and joins a room.
func JoinURL(code string) string { return "droplink://join/" + code }

// QR is a QR code as modules: Bits has Size*Size characters, row by row,
// '1' for dark. The UI draws it as pixel art.
type QR struct {
	Size int    `json:"size"`
	Bits string `json:"bits"`
	Text string `json:"text"`
}

// RoomQR encodes the join link for a room code.
func RoomQR(code string) (QR, error) {
	text := JoinURL(code)
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return QR{}, err
	}
	var b strings.Builder
	b.Grow(c.Size * c.Size)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
	}
	return QR{Size: c.Size, Bits: b.String(), Text: text}, nil
}

func registerQR(srv *server.Server) {
	srv.Handle("GET /api/rooms/{code}/qr", "qr", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := strings.ToUpper(r.PathValue("code"))
		if !signal.ValidCode(code) {
			server.WriteError(w, http.StatusBadRequest, "not a room code")
			return
		}
		q, err := RoomQR(code)
		if err != nil {
			server.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		server.WriteJSON(w, http.StatusOK, q)
	}))
}
