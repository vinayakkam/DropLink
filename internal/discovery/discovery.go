package discovery

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

// DefaultGroup is the multicast group and port every device uses.
var DefaultGroup = &net.UDPAddr{IP: net.IPv4(239, 255, 42, 99), Port: 47474}

// Options tune a Discovery. Zero values mean the defaults.
type Options struct {
	Group  *net.UDPAddr  // multicast group (DefaultGroup)
	Every  time.Duration // announce interval (2 s)
	TTL    time.Duration // a peer silent this long has left (7 s)
	Rescan time.Duration // interface re-scan interval (5 s)

	// ListenAddr, when set, listens on this unicast address instead of
	// joining the group, and Targets get every beacon too. Tests use
	// these to run several devices on loopback.
	ListenAddr  string
	Targets     []*net.UDPAddr
	NoBroadcast bool // do not also send to subnet broadcast addresses

	Log *slog.Logger

	fingerprint func() string // tests fake network changes
}

func (o *Options) defaults() {
	if o.Group == nil {
		o.Group = DefaultGroup
	}
	if o.Every <= 0 {
		o.Every = 2 * time.Second
	}
	if o.TTL <= 0 {
		o.TTL = 7 * time.Second
	}
	if o.Rescan <= 0 {
		o.Rescan = 5 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
}

// Discovery announces this device and keeps the radar of peers.
type Discovery struct {
	opts Options

	mu   sync.Mutex
	self Beacon
	kick chan struct{} // announce now (profile or port changed)

	table *table
}

// New returns a Discovery for self. port is the LAN transport's TCP port
// (0 until it is known; see SetPort).
func New(self contract.Peer, port uint16, opts Options) *Discovery {
	opts.defaults()
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	return &Discovery{
		opts: opts,
		self: Beacon{ID: self.ID, Name: self.Name, Avatar: self.Avatar & 15, Port: port,
			Nonce: binary.BigEndian.Uint64(nonce[:])},
		kick:  make(chan struct{}, 1),
		table: newTable(self.ID, opts.TTL),
	}
}

// SetPort updates the advertised LAN port and announces at once.
func (d *Discovery) SetPort(port uint16) {
	d.mu.Lock()
	d.self.Port = port
	d.mu.Unlock()
	d.announceSoon()
}

// SetProfile updates the advertised name and avatar and announces at once.
func (d *Discovery) SetProfile(name string, avatar uint8) {
	d.mu.Lock()
	d.self.Name, d.self.Avatar = name, avatar&15
	d.mu.Unlock()
	d.announceSoon()
}

func (d *Discovery) announceSoon() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// Run announces and listens until ctx ends. All goroutines it starts
// have stopped when it returns.
func (d *Discovery) Run(ctx context.Context) error {
	send, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return err
	}
	defer send.Close()

	var wg sync.WaitGroup
	var recv *net.UDPConn
	startRecv := func() error {
		c, err := d.listen()
		if err != nil {
			return err
		}
		recv = c
		wg.Add(1)
		go func() { defer wg.Done(); d.recvLoop(c) }()
		return nil
	}
	if err := startRecv(); err != nil {
		return err
	}
	defer func() {
		if recv != nil {
			recv.Close() // ends recvLoop
		}
		wg.Wait()
	}()

	tick := time.NewTicker(d.opts.Every)
	defer tick.Stop()
	sweep := time.NewTicker(max(d.opts.TTL/7, 100*time.Millisecond))
	defer sweep.Stop()
	rescan := time.NewTicker(d.opts.Rescan)
	defer rescan.Stop()
	fp := interfaceFingerprint()
	if d.opts.fingerprint != nil {
		fp = d.opts.fingerprint()
	}
	targets := d.targets()
	d.announce(send, targets)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sweep.C:
			d.table.expire()
		case <-tick.C:
			d.announce(send, targets)
		case <-d.kick:
			d.announce(send, targets)
		case <-rescan.C:
			now := interfaceFingerprint()
			if d.opts.fingerprint != nil {
				now = d.opts.fingerprint()
			}
			if now == fp {
				continue
			}
			// Wi-Fi switched, VPN up, cable plugged: rejoin the group on
			// the new default interface and announce on the new subnets.
			fp = now
			d.opts.Log.Info("discovery: network changed, rejoining")
			if d.opts.ListenAddr == "" {
				if recv != nil {
					recv.Close()
					recv = nil
				}
				if err := startRecv(); err != nil {
					// No network right now (e.g. Wi-Fi off): try again next scan.
					d.opts.Log.Warn("discovery: rejoin failed", "err", err)
					fp = ""
				}
			}
			targets = d.targets()
			d.announce(send, targets)
		}
	}
}

// interfaceFingerprint changes whenever an interface goes up or down or
// gets a different address.
func interfaceFingerprint() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var b []byte
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 {
			continue
		}
		b = append(b, ifi.Name...)
		b = append(b, '=')
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			b = append(b, a.String()...)
			b = append(b, ',')
		}
		b = append(b, ';')
	}
	return string(b)
}

// listen opens the receive socket: joined to the group, or on
// Options.ListenAddr for tests.
func (d *Discovery) listen() (*net.UDPConn, error) {
	if d.opts.ListenAddr != "" {
		addr, err := net.ResolveUDPAddr("udp4", d.opts.ListenAddr)
		if err != nil {
			return nil, err
		}
		return net.ListenUDP("udp4", addr)
	}
	// shortcut: the standard library joins the group on the default
	// interface only; subnet broadcasts (targets) reach the others. Use
	// golang.org/x/net/ipv4 for per-interface joins if that proves short.
	return net.ListenMulticastUDP("udp4", nil, d.opts.Group)
}

// targets is where each beacon goes: the group, every up interface's
// subnet broadcast address, and Options.Targets.
func (d *Discovery) targets() []*net.UDPAddr {
	out := []*net.UDPAddr{}
	if d.opts.ListenAddr == "" {
		out = append(out, d.opts.Group)
	}
	if !d.opts.NoBroadcast && d.opts.ListenAddr == "" {
		for _, ip := range broadcastAddrs() {
			out = append(out, &net.UDPAddr{IP: ip, Port: d.opts.Group.Port})
		}
	}
	return append(out, d.opts.Targets...)
}

func (d *Discovery) announce(send *net.UDPConn, targets []*net.UDPAddr) {
	d.mu.Lock()
	b := d.self
	d.mu.Unlock()
	var buf [MaxBeacon]byte
	pkt, err := b.Encode(buf[:0])
	if err != nil {
		d.opts.Log.Warn("discovery: cannot encode beacon", "err", err)
		return
	}
	for _, t := range targets {
		if _, err := send.WriteToUDP(pkt, t); err != nil {
			d.opts.Log.Debug("discovery: send", "to", t, "err", err)
		}
	}
}

func (d *Discovery) recvLoop(c *net.UDPConn) {
	buf := make([]byte, MaxBeacon+1) // +1 so oversized packets are seen as such
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue // e.g. ICMP-induced errors on Windows; keep listening
		}
		b, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		d.table.observe(b, from.IP)
	}
}

func beaconPeer(b Beacon, ip net.IP, now time.Time) contract.Peer {
	p := contract.Peer{ID: b.ID, Name: b.Name, Avatar: b.Avatar, Route: contract.RouteLAN, LastSeen: now}
	if b.Port != 0 {
		p.Addr = net.JoinHostPort(ip.String(), itoa(b.Port))
	}
	return p
}

func itoa(p uint16) string {
	var b [5]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + p%10)
		p /= 10
		if p == 0 {
			return string(b[i:])
		}
	}
}

// broadcastAddrs lists the directed broadcast address of every up,
// non-loopback IPv4 interface.
func broadcastAddrs() []net.IP {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 || ifi.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || len(ipn.Mask) != net.IPv4len {
				continue
			}
			bc := make(net.IP, 4)
			for i := range 4 {
				bc[i] = ip4[i] | ^ipn.Mask[i]
			}
			out = append(out, bc)
		}
	}
	return out
}

// PortFromEnv reads DROPLINK_LAN_PORT (0 when unset or invalid). The LAN
// transport sets the real port with SetPort once it listens.
func PortFromEnv() uint16 {
	n, err := strconv.ParseUint(os.Getenv("DROPLINK_LAN_PORT"), 10, 16)
	if err != nil {
		return 0
	}
	return uint16(n)
}
