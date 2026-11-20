// Package contract is the fixed boundary between P1 (Vinayak: engine, UI,
// desktop) and P2 (Kashish: crypto, discovery, transports, signaling).
//
// Rules (also in docs/TASKS.md, "Contracts"):
//   - Names and method sets here never change without both people agreeing.
//     New optional behaviour goes in a new interface checked with a type
//     assertion, so old implementations keep compiling.
//   - Every interface has an in-memory version in internal/mock. The UI and
//     the transfer engine are built and tested against those, so neither side
//     waits for the other.
//   - Implementations must stream: no method may buffer a whole file.
package contract

import (
	"context"
	"io"
	"net"
	"time"
)

// DeviceID identifies a device: lowercase hex of the first 16 bytes of
// SHA-256 over its public identity key (p2/crypto).
type DeviceID string

// Route is how a peer is reached.
type Route string

const (
	RouteLAN    Route = "lan"
	RouteWebRTC Route = "webrtc"
)

// Peer is another DropLink device as the radar shows it.
type Peer struct {
	ID       DeviceID  `json:"id"`
	Name     string    `json:"name"`
	Avatar   uint8     `json:"avatar"` // ship sprite index, 0..15
	Route    Route     `json:"route"`
	Addr     string    `json:"addr,omitempty"` // host:port, LAN only
	Trusted  bool      `json:"trusted"`
	LastSeen time.Time `json:"lastSeen"`
}

// ---- Discovery (p2/lan-discovery) ----------------------------------------

// PeerEventKind says what happened to a peer.
type PeerEventKind string

const (
	PeerJoined  PeerEventKind = "joined"
	PeerUpdated PeerEventKind = "updated"
	PeerLeft    PeerEventKind = "left"
)

// PeerEvent is one change on the radar.
type PeerEvent struct {
	Kind PeerEventKind `json:"kind"`
	Peer Peer          `json:"peer"`
}

// Discovery finds peers on the local network.
type Discovery interface {
	// Run announces this device and listens for others until ctx ends.
	Run(ctx context.Context) error
	// Peers returns the peers seen recently, sorted by name.
	Peers() []Peer
	// Subscribe delivers changes. A slow subscriber loses events, never
	// blocks discovery. cancel closes the channel.
	Subscribe() (events <-chan PeerEvent, cancel func())
}

// ---- Crypto (p2/crypto) ---------------------------------------------------

// SAS is the short authentication string: four glyph indices (0..63) both
// screens draw as pixel glyphs so people can compare them.
type SAS [4]uint8

// TrustedDevice is one entry in the trust list.
type TrustedDevice struct {
	ID    DeviceID  `json:"id"`
	Name  string    `json:"name"`
	Added time.Time `json:"added"`
}

// SecureConn is an authenticated, encrypted connection.
type SecureConn interface {
	net.Conn
	RemoteID() DeviceID
	SAS() SAS
}

// Crypto owns this device's identity key and the trust list.
type Crypto interface {
	ID() DeviceID
	// Secure runs the handshake over conn and returns the encrypted conn.
	Secure(ctx context.Context, conn net.Conn, initiator bool) (SecureConn, error)
	Trust(d TrustedDevice) error
	Untrust(id DeviceID) error
	Trusted() []TrustedDevice
	IsTrusted(id DeviceID) bool
}

// ---- Transports (p2/lan-transfer, p2/webrtc) ------------------------------

// Stream is one reliable, ordered, encrypted byte stream to a peer.
// Close must unblock pending Read and Write calls.
type Stream interface {
	io.ReadWriteCloser
	Remote() Peer
}

// Transport opens and accepts Streams over one route.
type Transport interface {
	Route() Route
	Dial(ctx context.Context, to Peer) (Stream, error)
	// Accept blocks until a peer opens a stream or ctx ends.
	Accept(ctx context.Context) (Stream, error)
	Close() error
}

// ---- Signaling (p2/signaling) ---------------------------------------------

// SignalKind is the type of a signaling message.
type SignalKind string

const (
	SignalHello     SignalKind = "hello"
	SignalOffer     SignalKind = "offer"
	SignalAnswer    SignalKind = "answer"
	SignalCandidate SignalKind = "candidate"
	SignalBye       SignalKind = "bye"
)

// Signal is relayed by the signaling hub and never stored. Data is opaque
// to the hub (SDP or an ICE candidate).
type Signal struct {
	Kind SignalKind `json:"kind"`
	From DeviceID   `json:"from"`
	To   DeviceID   `json:"to,omitempty"` // empty: everyone in the room
	Data []byte     `json:"data,omitempty"`
}

// Room is a joined signaling room.
type Room interface {
	Code() string
	Members() []Peer
	Send(ctx context.Context, s Signal) error
	// Signals is closed when the room is closed or the hub drops us.
	Signals() <-chan Signal
	Close() error
}

// Signaler joins rooms on a signaling hub. room "" creates a new room.
type Signaler interface {
	Join(ctx context.Context, room string, self Peer) (Room, error)
}

// ---- Transfers (engine: p1/transfer-engine) --------------------------------

// TransferID identifies one transfer on both ends.
type TransferID string

// FileMeta describes one file. Path is slash-separated and relative; the
// engine rejects absolute paths and "..".
type FileMeta struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Offer is what a sender proposes. Text is set for clipboard shares, which
// carry no files.
type Offer struct {
	ID    TransferID `json:"id"`
	From  Peer       `json:"from"`
	Files []FileMeta `json:"files"`
	Text  string     `json:"text,omitempty"`
}

// TotalSize is the sum of all file sizes.
func (o Offer) TotalSize() int64 {
	var n int64
	for _, f := range o.Files {
		n += f.Size
	}
	return n
}

// TransferState is where a transfer is in its life.
type TransferState string

const (
	StateOffered   TransferState = "offered"
	StateActive    TransferState = "active"
	StatePaused    TransferState = "paused"
	StateDone      TransferState = "done"
	StateFailed    TransferState = "failed"
	StateCancelled TransferState = "cancelled"
	StateRejected  TransferState = "rejected"
)

// Progress is reported at most a few times per second per transfer.
type Progress struct {
	ID          TransferID    `json:"id"`
	State       TransferState `json:"state"`
	File        int           `json:"file"` // index into Offer.Files
	Done        int64         `json:"done"` // bytes across all files
	Total       int64         `json:"total"`
	BytesPerSec float64       `json:"bytesPerSec"`
	Err         string        `json:"error,omitempty"`
}

// Source opens files the sender offered.
type Source interface {
	Open(f FileMeta) (io.ReadSeekCloser, error)
}

// Sink stores received files. Open returns a writer positioned after the
// bytes already on disk (have), so an interrupted transfer resumes there.
// When have > 0 the writer must also implement io.ReaderAt, so the engine
// can hash the bytes it already has; otherwise the engine restarts the file.
// Commit is called once the file's hash matched, Abort when it did not.
type Sink interface {
	Open(f FileMeta) (w io.WriteCloser, have int64, err error)
	Commit(f FileMeta) error
	Abort(f FileMeta, cause error) error
}

// ---- Routing and history (p2/resilience) -----------------------------------

// Router picks how to reach a peer (LAN first, then WebRTC, then relay)
// and retries with backoff. The transfer manager dials only through it.
type Router interface {
	Dial(ctx context.Context, to Peer) (Stream, error)
}

// Record is one finished transfer in the local history. It never holds
// file contents, only names and sizes.
type Record struct {
	ID        TransferID    `json:"id"`
	Peer      Peer          `json:"peer"`
	Outgoing  bool          `json:"outgoing"`
	Files     []FileMeta    `json:"files"`
	Text      bool          `json:"text"` // clipboard share; the text itself is not kept
	State     TransferState `json:"state"`
	Bytes     int64         `json:"bytes"`
	StartedAt time.Time     `json:"startedAt"`
	EndedAt   time.Time     `json:"endedAt"`
}

// History is the opt-in local transfer log, capped in size.
type History interface {
	Append(r Record) error
	// List returns up to limit records, newest first.
	List(limit int) ([]Record, error)
	Clear() error
}

// Engine moves an Offer's files over a Stream.
type Engine interface {
	Send(ctx context.Context, s Stream, o Offer, src Source, progress func(Progress)) error
	// Receive reads the offer, asks accept, and stores files into dst.
	Receive(ctx context.Context, s Stream, accept func(Offer) bool, dst Sink, progress func(Progress)) (Offer, error)
}
