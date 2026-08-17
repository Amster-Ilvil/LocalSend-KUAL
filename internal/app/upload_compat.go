package app

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// uploadCompatListener is deliberately placed below net/http. It only rewrites
// the malformed upload framing observed from current LocalSend v2.2 streaming clients.
// Windows/legacy requests are passed through byte-for-byte.
type uploadCompatListener struct {
	net.Listener
	server *Server
}

func newUploadCompatListener(inner net.Listener, server *Server) net.Listener {
	return &uploadCompatListener{Listener: inner, server: server}
}

func (l *uploadCompatListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &uploadCompatConn{Conn: c, server: l.server}, nil
}

type uploadCompatConn struct {
	net.Conn
	server  *Server
	once    sync.Once
	initErr error
	buf     []byte
	off     int
}

func (c *uploadCompatConn) Read(p []byte) (int, error) {
	c.once.Do(c.init)
	if c.initErr != nil {
		return 0, c.initErr
	}
	if c.off < len(c.buf) {
		n := copy(p, c.buf[c.off:])
		c.off += n
		return n, nil
	}
	return c.Conn.Read(p)
}

func (c *uploadCompatConn) init() {
	const maxHeader = 64 << 10
	tmp := make([]byte, 4096)
	var b bytes.Buffer
	headerEnd := -1
	for b.Len() < maxHeader {
		n, err := c.Conn.Read(tmp)
		if n > 0 {
			_, _ = b.Write(tmp[:n])
			if i := bytes.Index(b.Bytes(), []byte("\r\n\r\n")); i >= 0 {
				headerEnd = i + 4
				break
			}
		}
		if err != nil {
			c.initErr = err
			return
		}
	}
	if headerEnd < 0 {
		c.initErr = fmt.Errorf("HTTP header too large or incomplete")
		return
	}

	raw := b.Bytes()
	header := append([]byte(nil), raw[:headerEnd]...)
	bodyPrefix := append([]byte(nil), raw[headerEnd:]...)

	candidate, expected, sid, fid := c.server.streamUploadCompatCandidate(header, c.RemoteAddr())
	if !candidate {
		c.buf = append(header, bodyPrefix...)
		return
	}
	rawCLOriginal := headerValue(header, "Content-Length")
	if rawCLOriginal == "" {
		rawCLOriginal = "<missing>"
	}

	// If the sender used Expect: 100-continue, it will not send the body until
	// net/http asks for it. In that case use the declared session size so the
	// standard server can drive 100-continue normally.
	expect := strings.ToLower(headerValue(header, "Expect"))
	mode := "raw"
	if strings.Contains(expect, "100-continue") {
		header = rewriteContentLength(header, expected)
	} else {
		// Current LocalSend v2.2 streaming clients have been observed to write Content-Length: 0
		// while actually streaming HTTP/1.1 chunk frames. Read just enough of
		// the prefix to distinguish chunk framing from a raw file stream.
		mode = classifyCompatBody(bodyPrefix)
		if mode == "unknown" {
			_ = c.Conn.SetReadDeadline(time.Now().Add(1200 * time.Millisecond))
			for len(bodyPrefix) < 64 && mode == "unknown" {
				n, err := c.Conn.Read(tmp)
				if n > 0 {
					bodyPrefix = append(bodyPrefix, tmp[:n]...)
					mode = classifyCompatBody(bodyPrefix)
				}
				if err != nil {
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						break
					}
					c.initErr = err
					_ = c.Conn.SetReadDeadline(time.Time{})
					return
				}
			}
			_ = c.Conn.SetReadDeadline(time.Time{})
		}
		if mode == "unknown" {
			// v2.2's streaming client is chunked on HTTP/1.1; prefer that when
			// the prefix arrived too slowly to classify, rather than exposing
			// chunk-size bytes to the checksum/file writer.
			mode = "chunked"
		}
		if mode == "chunked" {
			header = rewriteAsChunked(header)
		} else {
			header = rewriteContentLength(header, expected)
		}
	}

	c.server.logger.Printf("v2.2 stream upload framing normalized before HTTP parse: remote=%s session=%s file=%s mode=%s expected=%d buffered=%d rawContentLength=%s", hostOnly(c.RemoteAddr()), sid, fid, mode, expected, len(bodyPrefix), rawCLOriginal)
	c.buf = append(header, bodyPrefix...)
}

func hostOnly(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err == nil {
		return host
	}
	return addr.String()
}

func (s *Server) streamUploadCompatCandidate(header []byte, remote net.Addr) (bool, int64, string, string) {
	lines := bytes.Split(header, []byte("\r\n"))
	if len(lines) == 0 {
		return false, 0, "", ""
	}
	parts := strings.SplitN(string(lines[0]), " ", 3)
	if len(parts) < 2 || parts[0] != "POST" {
		return false, 0, "", ""
	}
	u, err := url.ParseRequestURI(parts[1])
	if err != nil || u.Path != "/api/localsend/v2/upload" {
		return false, 0, "", ""
	}
	// Affected LocalSend v2.2 streaming clients have been observed in two
	// wire forms: an explicit `Content-Length: 0`, or no Content-Length at
	// all. Go normalizes both to Request.ContentLength == 0, but the latter
	// bypassed the v0.1.6 shim because we required a literal zero header.
	// Accept only missing/zero CL with no Transfer-Encoding; fixed-length
	// senders (including the verified Windows 2.1 path) remain byte-for-byte
	// untouched.
	rawCL := strings.TrimSpace(headerValue(header, "Content-Length"))
	if (rawCL != "" && rawCL != "0") || headerValue(header, "Transfer-Encoding") != "" {
		return false, 0, "", ""
	}
	sid := u.Query().Get("sessionId")
	fid := u.Query().Get("fileId")
	token := u.Query().Get("token")
	if sid == "" || fid == "" || token == "" {
		return false, 0, "", ""
	}
	rip := hostOnly(remote)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil || s.session.Cancelled || s.session.ID != sid || s.session.Sender.IP != rip {
		return false, 0, "", ""
	}
	version := s.session.Sender.Version
	if compareProtocolVersion(version, "2.2") < 0 {
		return false, 0, "", ""
	}
	fs := s.session.Files[fid]
	if fs == nil || fs.Token != token || fs.Info.Size <= 0 {
		return false, 0, "", ""
	}
	return true, fs.Info.Size, sid, fid
}

func compareProtocolVersion(a, b string) int {
	parse := func(v string) (int, int) {
		var major, minor int
		_, _ = fmt.Sscanf(v, "%d.%d", &major, &minor)
		return major, minor
	}
	am, an := parse(a)
	bm, bn := parse(b)
	if am < bm || (am == bm && an < bn) {
		return -1
	}
	if am > bm || (am == bm && an > bn) {
		return 1
	}
	return 0
}

func headerValue(header []byte, name string) string {
	want := strings.ToLower(name)
	sc := bufio.NewScanner(bytes.NewReader(header))
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			continue
		}
		if line == "" {
			break
		}
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		if strings.ToLower(strings.TrimSpace(line[:i])) == want {
			return strings.TrimSpace(line[i+1:])
		}
	}
	return ""
}

func rewriteContentLength(header []byte, size int64) []byte {
	return rewriteFramingHeader(header, "Content-Length", strconv.FormatInt(size, 10))
}

func rewriteAsChunked(header []byte) []byte {
	out := rewriteFramingHeader(header, "Content-Length", "")
	marker := []byte("\r\n\r\n")
	i := bytes.Index(out, marker)
	if i < 0 {
		return out
	}
	insert := []byte("Transfer-Encoding: chunked\r\n")
	res := make([]byte, 0, len(out)+len(insert))
	res = append(res, out[:i+2]...)
	res = append(res, insert...)
	res = append(res, out[i+2:]...)
	return res
}

func rewriteFramingHeader(header []byte, name, value string) []byte {
	marker := []byte("\r\n\r\n")
	end := bytes.Index(header, marker)
	if end < 0 {
		return header
	}
	// Work only inside the header block. v0.1.6 inserted a newly-created
	// Content-Length before the *last* empty split element, which for a header
	// ending in CRLFCRLF placed it after the terminator (inside the body) and
	// produced a 400 on real clients that omitted Content-Length entirely.
	lines := strings.Split(string(header[:end]), "\r\n")
	want := strings.ToLower(name)
	out := make([]string, 0, len(lines)+1)
	replaced := false
	for _, line := range lines {
		if i := strings.IndexByte(line, ':'); i > 0 && strings.ToLower(strings.TrimSpace(line[:i])) == want {
			if value != "" && !replaced {
				out = append(out, name+": "+value)
				replaced = true
			}
			continue
		}
		out = append(out, line)
	}
	if value != "" && !replaced {
		out = append(out, name+": "+value)
	}
	return []byte(strings.Join(out, "\r\n") + "\r\n\r\n")
}

// classifyCompatBody recognizes RFC 7230 chunk-size framing. "raw" means the
// bytes cannot be a chunk-size line and should be treated as file bytes.
func classifyCompatBody(prefix []byte) string {
	if len(prefix) == 0 {
		return "unknown"
	}
	limit := len(prefix)
	if limit > 64 {
		limit = 64
	}
	for i := 0; i < limit; i++ {
		b := prefix[i]
		if b == '\r' {
			if i+1 >= len(prefix) {
				return "unknown"
			}
			if prefix[i+1] != '\n' || i == 0 {
				return "raw"
			}
			line := string(prefix[:i])
			if semi := strings.IndexByte(line, ';'); semi >= 0 {
				line = line[:semi]
			}
			if line == "" {
				return "raw"
			}
			if _, err := strconv.ParseUint(strings.TrimSpace(line), 16, 64); err == nil {
				return "chunked"
			}
			return "raw"
		}
		if b == ';' {
			continue
		}
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return "raw"
		}
	}
	if len(prefix) >= 64 {
		return "raw"
	}
	return "unknown"
}

// compile-time assertions used by tests and to keep imports honest.
var _ io.Reader = (*uploadCompatConn)(nil)
