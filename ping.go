package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	icmpEchoRequest4 = 8
	icmpEchoReply4   = 0
	icmpEchoRequest6 = 128
	icmpEchoReply6   = 129
)

// icmpClient пінгує один хост через raw ICMP сокет.
// Створюється по одному на хост, сокет пере-використовується
// між спробами (немає фрагментації fd) і перестворюється при збоях.
// Якщо raw сокет недоступний (немає CAP_NET_RAW) — фолбек
// на системну утиліту ping.
type icmpClient struct {
	log  *Logger
	name string

	mu      sync.Mutex
	conn    net.Conn
	fam     int // 4 або 6
	id      uint16
	seq     uint16
	binPath string
	useBin  bool
}

func newICMPClient(log *Logger, name string) *icmpClient {
	c := &icmpClient{
		log:  log,
		name: name,
		id:   uint16(os.Getpid()),
		fam:  4,
	}
	c.binPath, _ = findPing()
	return c
}

func (c *icmpClient) ping(ctx context.Context, host string, timeout time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.useBin {
		return c.binPing(ctx, host, timeout)
	}
	if c.conn == nil {
		if err := c.open(host); err != nil {
			if c.binPath != "" {
				c.log.Warnf("host %s: raw ICMP unavailable (%v), falling back to system ping %q", c.name, err, c.binPath)
				c.useBin = true
				return c.binPing(ctx, host, timeout)
			}
			return fmt.Errorf("icmp: %w", err)
		}
	}
	return c.rawPing(host, timeout)
}

func (c *icmpClient) open(host string) error {
	ip, fam, err := resolveIP(host)
	if err != nil {
		return err
	}
	network := "ip4:icmp"
	if fam == 6 {
		network = "ip6:icmp"
	}
	var d net.Dialer
	conn, err := d.Dial(network, ip.String())
	if err != nil {
		return fmt.Errorf("open %s socket: %w", network, err)
	}
	c.conn = conn
	c.fam = fam
	c.seq = 0
	return nil
}

func (c *icmpClient) rawPing(host string, timeout time.Duration) error {
	c.seq++
	seq := c.seq
	req := c.buildRequest(seq)
	_ = c.conn.SetDeadline(time.Now().Add(timeout))
	if _, err := c.conn.Write(req); err != nil {
		c.resetLocked()
		if isPermission(err) && c.binPath != "" {
			c.log.Warnf("host %s: raw ICMP permission denied, falling back to system ping %q", c.name, c.binPath)
			c.useBin = true
		}
		return fmt.Errorf("icmp send to %s: %w", host, err)
	}
	buf := make([]byte, 1600)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			return fmt.Errorf("icmp read from %s: %w", host, err)
		}
		if c.matchReply(buf[:n], seq) {
			return nil
		}
		// чужий пакет — читаємо далі до дедлайну
	}
}

func (c *icmpClient) matchReply(data []byte, seq uint16) bool {
	b := data
	if c.fam == 4 {
		// для raw IPv4 сокетів ядро віддає пакет разом з IP-заголовком
		if len(b) >= 20 && b[0]>>4 == 4 {
			ihl := int(b[0]&0x0f) * 4
			if ihl < 20 || len(b) <= ihl {
				return false
			}
			b = b[ihl:]
		}
		if len(b) < 8 || b[0] != icmpEchoReply4 {
			return false
		}
	} else {
		if len(b) < 8 || b[0] != icmpEchoReply6 {
			return false
		}
	}
	id := binary.BigEndian.Uint16(b[4:6])
	return id == c.id && binary.BigEndian.Uint16(b[6:8]) == seq
}

func (c *icmpClient) buildRequest(seq uint16) []byte {
	typ := byte(icmpEchoRequest4)
	if c.fam == 6 {
		typ = icmpEchoRequest6
	}
	pkt := make([]byte, 8+32)
	pkt[0] = typ
	binary.BigEndian.PutUint16(pkt[4:6], c.id)
	binary.BigEndian.PutUint16(pkt[6:8], seq)
	binary.BigEndian.PutUint64(pkt[8:16], uint64(time.Now().UnixMilli()))
	binary.BigEndian.PutUint16(pkt[2:4], icmpSum(pkt))
	return pkt
}

func (c *icmpClient) resetLocked() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *icmpClient) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked()
}

func (c *icmpClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetLocked()
}

func (c *icmpClient) binPing(ctx context.Context, host string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	cctx, cancel := context.WithTimeout(ctx, timeout+3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, c.binPath, "-c", "1", "-W", strconv.Itoa(secs), host)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ping %s: timeout", host)
		}
		return fmt.Errorf("ping %s: %w", host, err)
	}
	return nil
}

// tcpPing — альтернатива для хостів, де ICMP закритий/недоступний:
// успіх = TCP-з'єднання встановилось.
func tcpPing(ctx context.Context, host string, port int, timeout time.Duration) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var d net.Dialer
	d.Timeout = timeout
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err == nil {
		conn.Close()
	}
	return err
}

func resolveIP(host string) (net.IP, int, error) {
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return ip, 4, nil
		}
		return ip, 6, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return nil, 0, fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, 4, nil
		}
	}
	return ips[0], 6, nil
}

func findPing() (string, bool) {
	if path, err := exec.LookPath("ping"); err == nil {
		return path, true
	}
	for _, p := range []string{"/bin/ping", "/usr/bin/ping", "/sbin/ping", "/usr/sbin/ping"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

func isPermission(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "permission")
}

func icmpSum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum >> 16) + (sum & 0xffff)
	}
	return ^uint16(sum)
}
