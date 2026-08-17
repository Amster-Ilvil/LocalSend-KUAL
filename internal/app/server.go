package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	root        string
	cfg         Config
	identity    *Identity
	state       *StateStore
	logger      *log.Logger
	mu          sync.Mutex
	session     *uploadSession
	announce    chan struct{}
	fingerprint string
}

type loggingListener struct {
	net.Listener
	logger *log.Logger
}

func (l *loggingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.logger.Printf("tcp accepted: remote=%s local=%s", c.RemoteAddr(), c.LocalAddr())
	}
	return c, err
}

func NewServer(root string, cfg Config, id *Identity, state *StateStore, logger *log.Logger) *Server {
	fingerprint := id.Fingerprint
	if !cfg.Encryption {
		if fp, err := LoadOrCreateHTTPFingerprint(root); err == nil {
			fingerprint = fp
		} else if logger != nil {
			logger.Printf("HTTP fingerprint fallback to certificate fingerprint: %v", err)
		}
	}
	return &Server{root: root, cfg: cfg, identity: id, state: state, logger: logger, announce: make(chan struct{}, 1), fingerprint: fingerprint}
}

func (s *Server) ownInfo() DeviceInfo {
	proto := "http"
	if s.cfg.Encryption {
		proto = "https"
	}
	return DeviceInfo{
		Alias:       s.cfg.Alias,
		Version:     ProtocolVersion,
		DeviceModel: "Kindle Voyage",
		DeviceType:  "mobile",
		Fingerprint: s.fingerprint,
		Port:        s.cfg.Port,
		Protocol:    proto,
		Download:    false,
	}
}

func (s *Server) responseInfo() DeviceResponse {
	info := s.ownInfo()
	return DeviceResponse{
		Alias:       info.Alias,
		Version:     info.Version,
		DeviceModel: info.DeviceModel,
		DeviceType:  info.DeviceType,
		Fingerprint: info.Fingerprint,
		Download:    info.Download,
	}
}

func (s *Server) Run(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(s.root, "state"), 0o700); err != nil {
		return err
	}
	pidPath := filepath.Join(s.root, "state", "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	s.logger.Printf("starting LocalSend-KUAL v0.1.7 pid=%d uid=%d protocol=%s port=%d interface=%s receive=%s fingerprint=%s", os.Getpid(), os.Geteuid(), s.ownInfo().Protocol, s.cfg.Port, s.cfg.Interface, s.cfg.ReceiveDir, s.ownInfo().Fingerprint)
	fw, fwErr := OpenFirewall(s.cfg.Interface, s.cfg.Port, s.logger)
	if fwErr != nil {
		s.logger.Printf("firewall setup failed (continuing for diagnosis): %v", fwErr)
	} else {
		s.logger.Printf("firewall temporary allow active: interface=%s tcp/udp=%d", s.cfg.Interface, s.cfg.Port)
		defer func() {
			if err := fw.Close(); err != nil {
				s.logger.Printf("firewall cleanup failed: %v", err)
			} else {
				s.logger.Printf("firewall temporary allow removed")
			}
		}()
	}
	st := RuntimeStatus{
		Running:     true,
		PID:         os.Getpid(),
		StartedAt:   time.Now().UTC(),
		Protocol:    s.ownInfo().Protocol,
		Port:        s.cfg.Port,
		Alias:       s.cfg.Alias,
		Fingerprint: s.fingerprint,
		ReceiveDir:  s.cfg.ReceiveDir,
		LastEvent:   "started",
		LastEventAt: time.Now().UTC(),
		PeerCount:   len(s.state.Peers()),
	}
	s.state.SetStatus(st)
	defer s.state.MarkStopped()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/localsend/v2/register", s.handleRegister)
	mux.HandleFunc("/api/localsend/v2/prepare-upload", s.handlePrepareUpload)
	mux.HandleFunc("/api/localsend/v2/upload", s.handleUpload)
	mux.HandleFunc("/api/localsend/v2/cancel", s.handleCancel)
	mux.HandleFunc("/api/localsend/v2/info", s.handleInfo)
	mux.HandleFunc("/_kindle/health", s.handleHealth)

	httpServer := &http.Server{
		Handler:           s.requestLogMiddleware(s.limitBodyMiddleware(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       30 * time.Second,
		ErrorLog:          s.logger,
	}
	ln, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", s.cfg.Port))
	if err != nil {
		return fmt.Errorf("listen TCP %d: %w", s.cfg.Port, err)
	}
	s.logger.Printf("tcp listener ready: 0.0.0.0:%d", s.cfg.Port)
	if !s.cfg.Encryption {
		// Normalize only the malformed macOS v2.2 upload framing before Go's
		// net/http parser sees it. Windows/legacy traffic is byte-for-byte pass-through.
		ln = newUploadCompatListener(ln, s)
	}
	if s.cfg.Encryption {
		tlsCfg := &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{s.identity.Cert},
			ClientAuth:   tls.RequireAnyClientCert,
			// Current desktop LocalSend advertises h2 and http/1.1. This
			// lightweight server intentionally speaks HTTP/1.1 only, so select it
			// explicitly instead of leaving ALPN ambiguous.
			NextProtos: []string{"http/1.1"},
		}
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifySelfSignedRaw(rawCerts)
		}
		ln = tls.NewListener(ln, tlsCfg)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(&loggingListener{Listener: ln, logger: s.logger})
	}()
	go func() {
		if err := s.runDiscovery(ctx); err != nil && ctx.Err() == nil {
			s.logger.Printf("multicast discovery unavailable: %v", err)
			s.state.Event("multicast unavailable; HTTP server still active", 0, 0)
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed || ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func (s *Server) TriggerAnnouncement() {
	select {
	case s.announce <- struct{}{}:
	default:
	}
}

func (s *Server) limitBodyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/localsend/v2/upload" {
			r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tlsMode := "plain"
		if r.TLS != nil {
			tlsMode = "tls"
		}
		s.logger.Printf("request begin: remote=%s method=%s path=%s proto=%s transport=%s contentLength=%d", remoteIP(r), r.Method, r.URL.Path, r.Proto, tlsMode, r.ContentLength)
		if r.URL.Path == "/api/localsend/v2/upload" && (r.ContentLength <= 0 || len(r.TransferEncoding) > 0) {
			s.logger.Printf("upload framing parsed: remote=%s contentLength=%d transferEncoding=%q", remoteIP(r), r.ContentLength, strings.Join(r.TransferEncoding, ","))
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func decodeJSON(r *http.Request, v any) error {
	// Be forward-compatible with newer LocalSend v2 senders. The official
	// protocol may gain optional fields; rejecting the entire request for an
	// unknown JSON member makes old receivers appear to hang/fail.
	dec := json.NewDecoder(r.Body)
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) verifyTLSFingerprint(r *http.Request, claimed string) bool {
	if !s.cfg.Encryption {
		return true
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return false
	}
	actual := certFingerprint(r.TLS.PeerCertificates[0].Raw)
	return strings.EqualFold(actual, claimed)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) {
		return
	}
	var info DeviceInfo
	if err := decodeJSON(r, &info); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if !s.verifyTLSFingerprint(r, info.Fingerprint) {
		http.Error(w, "fingerprint mismatch", http.StatusForbidden)
		return
	}
	if strings.EqualFold(info.Fingerprint, s.identity.Fingerprint) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	p := Peer{DeviceInfo: info, IP: remoteIP(r)}
	s.state.SavePeer(p, s.cfg.MaxPeers)
	s.state.Event("registered "+info.Alias, 0, 0)
	writeJSON(w, http.StatusOK, s.responseInfo())
}

func sameStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func samePrepareFiles(session *uploadSession, files map[string]FileInfo) bool {
	if session == nil || len(session.Files) != len(files) {
		return false
	}
	for id, existing := range session.Files {
		incoming, ok := files[id]
		if !ok || existing == nil {
			return false
		}
		if incoming.ID == "" {
			incoming.ID = id
		}
		e := existing.Info
		if incoming.ID != e.ID || incoming.FileName != e.FileName || incoming.Size != e.Size || incoming.FileType != e.FileType || !sameStringPtr(incoming.SHA256, e.SHA256) {
			return false
		}
	}
	return true
}

func sessionTokens(session *uploadSession) map[string]string {
	tokens := make(map[string]string, len(session.Files))
	for id, f := range session.Files {
		if f != nil {
			tokens[id] = f.Token
		}
	}
	return tokens
}

func (s *Server) handlePrepareUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) {
		return
	}
	if s.cfg.PIN != "" && r.URL.Query().Get("pin") != s.cfg.PIN {
		http.Error(w, "PIN required / invalid PIN", http.StatusUnauthorized)
		return
	}
	if !s.cfg.AutoAccept {
		http.Error(w, "receiver is not accepting transfers", http.StatusForbidden)
		return
	}
	var req PrepareUploadRequest
	if err := decodeJSON(r, &req); err != nil {
		s.logger.Printf("prepare-upload decode failed: remote=%s error=%v", remoteIP(r), err)
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if len(req.Files) == 0 {
		s.logger.Printf("prepare-upload rejected: remote=%s reason=no-files", remoteIP(r))
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if !s.verifyTLSFingerprint(r, req.Info.Fingerprint) {
		s.logger.Printf("prepare-upload rejected: remote=%s reason=fingerprint-mismatch alias=%q", remoteIP(r), req.Info.Alias)
		http.Error(w, "fingerprint mismatch", http.StatusForbidden)
		return
	}

	s.mu.Lock()
	if s.session != nil && !s.session.Cancelled {
		allDone := true
		for _, f := range s.session.Files {
			if !f.Received {
				allDone = false
				break
			}
		}
		if !allDone && time.Since(s.session.CreatedAt) < 30*time.Minute {
			// Some desktop LocalSend builds retry prepare-upload when the first
			// response is delayed/lost. Replaying the same request must return the
			// same session/tokens rather than turning a harmless retry into 409.
			if s.session.Sender.IP == remoteIP(r) && strings.EqualFold(s.session.Sender.Fingerprint, req.Info.Fingerprint) && samePrepareFiles(s.session, req.Files) {
				resp := PrepareUploadResponse{SessionID: s.session.ID, Files: sessionTokens(s.session)}
				sid := s.session.ID
				s.mu.Unlock()
				s.logger.Printf("prepare-upload replay: sender=%q version=%q model=%q ip=%s session=%s", req.Info.Alias, req.Info.Version, req.Info.DeviceModel, remoteIP(r), sid)
				writeJSON(w, http.StatusOK, resp)
				return
			}
			s.mu.Unlock()
			http.Error(w, "blocked by another session", http.StatusConflict)
			return
		}
	}
	defer s.mu.Unlock()

	sid, err := randomHex(16)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	files := make(map[string]*uploadFileState, len(req.Files))
	tokens := make(map[string]string, len(req.Files))
	for id, fi := range req.Files {
		if fi.ID == "" {
			fi.ID = id
		}
		if fi.ID != id || fi.Size < 0 {
			http.Error(w, "invalid file metadata", http.StatusBadRequest)
			return
		}
		if _, err := safeRelativePath(fi.FileName); err != nil {
			http.Error(w, "invalid file name", http.StatusBadRequest)
			return
		}
		token, err := randomHex(16)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		files[id] = &uploadFileState{Info: fi, Token: token}
		tokens[id] = token
	}
	peer := Peer{DeviceInfo: req.Info, IP: remoteIP(r), LastSeen: time.Now().UTC()}
	s.state.SavePeer(peer, s.cfg.MaxPeers)
	s.session = &uploadSession{ID: sid, Sender: peer, CreatedAt: time.Now(), Files: files}
	s.state.Event(fmt.Sprintf("accepted %d file(s) from %s", len(files), req.Info.Alias), 0, 0)
	s.logger.Printf("prepare-upload accepted: sender=%q version=%q model=%q protocol=%q ip=%s files=%d session=%s", req.Info.Alias, req.Info.Version, req.Info.DeviceModel, req.Info.Protocol, peer.IP, len(files), sid)
	writeJSON(w, http.StatusOK, PrepareUploadResponse{SessionID: sid, Files: tokens})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) {
		return
	}
	sid := r.URL.Query().Get("sessionId")
	fid := r.URL.Query().Get("fileId")
	token := r.URL.Query().Get("token")
	if sid == "" || fid == "" || token == "" {
		http.Error(w, "missing parameters", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	if s.session == nil || s.session.ID != sid || s.session.Cancelled {
		s.mu.Unlock()
		http.Error(w, "invalid session", http.StatusForbidden)
		return
	}
	if remoteIP(r) != s.session.Sender.IP {
		s.mu.Unlock()
		http.Error(w, "invalid IP", http.StatusForbidden)
		return
	}
	fs := s.session.Files[fid]
	if fs == nil || fs.Token != token {
		s.mu.Unlock()
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	if fs.Received {
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	fi := fs.Info
	s.mu.Unlock()

	dest, err := pathWithin(s.cfg.ReceiveDir, fi.FileName)
	if err != nil {
		http.Error(w, "invalid destination", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		http.Error(w, "cannot create destination", http.StatusInternalServerError)
		return
	}
	dest = uniquePath(dest)
	tmp := dest + ".localsend-part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		return
	}

	var hasher hash.Hash
	var writer io.Writer = out
	if s.cfg.VerifyChecksums && fi.SHA256 != nil && *fi.SHA256 != "" {
		hasher = sha256.New()
		writer = io.MultiWriter(out, hasher)
	}
	n, copyErr := io.CopyN(writer, r.Body, fi.Size)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || n != fi.Size {
		_ = os.Remove(tmp)
		http.Error(w, "incomplete upload", http.StatusBadRequest)
		return
	}
	// Reject extra bytes, if any.
	var extra [1]byte
	if m, _ := r.Body.Read(extra[:]); m != 0 {
		_ = os.Remove(tmp)
		http.Error(w, "upload larger than declared size", http.StatusBadRequest)
		return
	}
	if hasher != nil {
		actual := strings.ToLower(hex.EncodeToString(hasher.Sum(nil)))
		expected := strings.ToLower(strings.TrimSpace(*fi.SHA256))
		if actual != expected {
			_ = os.Remove(tmp)
			http.Error(w, "checksum mismatch", http.StatusUnprocessableEntity)
			return
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		http.Error(w, "cannot finalize file", http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	if s.session != nil && s.session.ID == sid {
		if cur := s.session.Files[fid]; cur != nil {
			cur.Received = true
			cur.SavedAs = dest
		}
	}
	s.mu.Unlock()
	s.state.Event("received "+filepath.Base(dest), 1, n)
	s.logger.Printf("received %s (%d bytes)", dest, n)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodPost) {
		return
	}
	sid := r.URL.Query().Get("sessionId")
	s.mu.Lock()
	if s.session != nil && s.session.ID == sid {
		s.session.Cancelled = true
	}
	s.mu.Unlock()
	s.state.Event("transfer cancelled", 0, 0)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.responseInfo())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, "ok\n")
}
