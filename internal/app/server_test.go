package app

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T, root string) Config {
	t.Helper()
	recv := filepath.Join(root, "recv")
	out := filepath.Join(root, "out")
	if err := os.MkdirAll(recv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	return Config{Alias: "Kindle Voyage", Port: 53317, Encryption: true, AutoAccept: true, VerifyChecksums: true, ReceiveDir: recv, OutboxDir: out, AnnounceSeconds: 30, MaxPeers: 12}
}

func makeTestTLSCert(t *testing.T, cn string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, leaf
}

func TestHTTPClientForPeerForcesClientCertDespiteCAHint(t *testing.T) {
	root := t.TempDir()
	clientID, err := LoadOrCreateIdentity(filepath.Join(root, "client"))
	if err != nil {
		t.Fatal(err)
	}
	serverCert, serverLeaf := makeTestTLSCert(t, "Mac LocalSend Server")
	_, unrelatedHint := makeTestTLSCert(t, "Unrelated CA Hint")
	pool := x509.NewCertPool()
	pool.AddCert(unrelatedHint)
	seenClient := make(chan bool, 1)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen := r.TLS != nil && len(r.TLS.PeerCertificates) > 0
		seenClient <- seen
		if !seen {
			http.Error(w, "client certificate missing", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	ts.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAnyClientCert,
		ClientCAs:    pool,
		NextProtos:   []string{"http/1.1"},
	}
	ts.StartTLS()
	defer ts.Close()

	peer := Peer{DeviceInfo: DeviceInfo{Protocol: "https", Fingerprint: certFingerprint(serverLeaf.Raw)}}
	client := HTTPClientForPeer(clientID, peer, 5*time.Second)
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("HTTPS request with forced LocalSend client certificate failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status = %d", resp.StatusCode)
	}
	select {
	case seen := <-seenClient:
		if !seen {
			t.Fatal("server did not receive client certificate")
		}
	case <-time.After(time.Second):
		t.Fatal("server did not observe request")
	}
}

func TestSafeRelativePath(t *testing.T) {
	good := []string{"book.epub", "folder/page 01.jpg", "中文/漫画.cbz"}
	for _, x := range good {
		if _, err := safeRelativePath(x); err != nil {
			t.Fatalf("%q rejected: %v", x, err)
		}
	}
	bad := []string{"../etc/passwd", "/etc/passwd", "a/../../b", "\x00bad"}
	for _, x := range bad {
		if _, err := safeRelativePath(x); err == nil {
			t.Fatalf("%q unexpectedly accepted", x)
		}
	}
}

func TestIdentityStableAndSelfSigned(t *testing.T) {
	root := t.TempDir()
	id1, err := LoadOrCreateIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := LoadOrCreateIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if id1.Fingerprint != id2.Fingerprint {
		t.Fatal("fingerprint changed across reload")
	}
	if err := verifySelfSignedRaw([][]byte{id1.CertDER}); err != nil {
		t.Fatalf("self-signed cert rejected: %v", err)
	}
	if id1.Cert.Leaf == nil || id1.Cert.Leaf.Subject.CommonName != "LocalSend User" {
		t.Fatal("unexpected certificate CN")
	}
}

func TestTLSPrepareAndUpload(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t, root)
	serverID, err := LoadOrCreateIdentity(filepath.Join(root, "server-id"))
	if err != nil {
		t.Fatal(err)
	}
	clientID, err := LoadOrCreateIdentity(filepath.Join(root, "client-id"))
	if err != nil {
		t.Fatal(err)
	}
	state := NewStateStore(root)
	s := NewServer(root, cfg, serverID, state, log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/localsend/v2/register", s.handleRegister)
	mux.HandleFunc("/api/localsend/v2/prepare-upload", s.handlePrepareUpload)
	mux.HandleFunc("/api/localsend/v2/upload", s.handleUpload)
	ts := httptest.NewUnstartedServer(s.limitBodyMiddleware(mux))
	ts.TLS = &tls.Config{
		MinVersion:            tls.VersionTLS12,
		Certificates:          []tls.Certificate{serverID.Cert},
		ClientAuth:            tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error { return verifySelfSignedRaw(rawCerts) },
	}
	ts.StartTLS()
	defer ts.Close()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{clientID.Cert}}}, Timeout: 5 * time.Second}
	payload := []byte("hello voyage")
	h := sha256.Sum256(payload)
	hash := hex.EncodeToString(h[:])
	info := DeviceInfo{Alias: "Test Mac", Version: "2.0", DeviceModel: "Mac", DeviceType: "desktop", Fingerprint: clientID.Fingerprint, Port: 53317, Protocol: "https"}
	preq := PrepareUploadRequest{Info: info, Files: map[string]FileInfo{"f1": {ID: "f1", FileName: "Books/hello.txt", Size: int64(len(payload)), FileType: "text/plain", SHA256: &hash}}}
	b, _ := json.Marshal(preq)
	resp, err := client.Post(ts.URL+"/api/localsend/v2/prepare-upload", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("prepare HTTP %d: %s", resp.StatusCode, body)
	}
	var prep PrepareUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&prep); err != nil {
		t.Fatal(err)
	}
	token := prep.Files["f1"]
	u := ts.URL + "/api/localsend/v2/upload?sessionId=" + prep.SessionID + "&fileId=f1&token=" + token
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(payload))
	req.ContentLength = int64(len(payload))
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("upload HTTP %d", resp2.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(cfg.ReceiveDir, "Books", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: %q", got)
	}
}

func TestKUALMenuGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menu.json")
	peers := []Peer{{DeviceInfo: DeviceInfo{Alias: "My Mac", Fingerprint: strings.Repeat("A", 64), Protocol: "https", Port: 53317}, IP: "192.168.1.20", LastSeen: time.Now()}}
	if err := WriteKUALMenu(path, peers); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "My Mac") || !strings.Contains(string(b), "LocalSend") {
		t.Fatal("generated menu missing peer or app label")
	}
}

func TestProtocolV22WireShape(t *testing.T) {
	if ProtocolVersion != "2.2" {
		t.Fatalf("protocol version = %q, want 2.2", ProtocolVersion)
	}
	root := t.TempDir()
	cfg := testConfig(t, root)
	id, err := LoadOrCreateIdentity(filepath.Join(root, "id"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(root, cfg, id, NewStateStore(root), log.New(io.Discard, "", 0))
	b, err := json.Marshal(s.ownInfo())
	if err != nil {
		t.Fatal(err)
	}
	wire := string(b)
	if strings.Contains(wire, "announce") {
		t.Fatalf("v2.2 multicast/register wire payload must not contain legacy announce field: %s", wire)
	}
	if !strings.Contains(wire, `"version":"2.2"`) {
		t.Fatalf("wire payload missing v2.2: %s", wire)
	}
}

func TestDefaultReceiveDirIsDocumentsRoot(t *testing.T) {
	cfg := DefaultConfig("ignored")
	if cfg.ReceiveDir != "/mnt/us/documents" {
		t.Fatalf("receive_dir = %q, want /mnt/us/documents", cfg.ReceiveDir)
	}
}

func TestTLSChunkedUploadReturns200(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t, root)
	serverID, err := LoadOrCreateIdentity(filepath.Join(root, "server-id"))
	if err != nil {
		t.Fatal(err)
	}
	clientID, err := LoadOrCreateIdentity(filepath.Join(root, "client-id"))
	if err != nil {
		t.Fatal(err)
	}
	state := NewStateStore(root)
	s := NewServer(root, cfg, serverID, state, log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/localsend/v2/prepare-upload", s.handlePrepareUpload)
	mux.HandleFunc("/api/localsend/v2/upload", s.handleUpload)
	ts := httptest.NewUnstartedServer(s.limitBodyMiddleware(mux))
	ts.TLS = &tls.Config{
		MinVersion:            tls.VersionTLS12,
		Certificates:          []tls.Certificate{serverID.Cert},
		ClientAuth:            tls.RequireAnyClientCert,
		NextProtos:            []string{"http/1.1"},
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error { return verifySelfSignedRaw(rawCerts) },
	}
	ts.StartTLS()
	defer ts.Close()

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{clientID.Cert}, NextProtos: []string{"http/1.1"}}}, Timeout: 5 * time.Second}
	payload := []byte(strings.Repeat("reqwest-stream-", 1024))
	info := DeviceInfo{Alias: "Windows", Version: "2.2", DeviceModel: "Windows", DeviceType: "desktop", Fingerprint: clientID.Fingerprint, Port: 53317, Protocol: "https"}
	preq := PrepareUploadRequest{Info: info, Files: map[string]FileInfo{"f1": {ID: "f1", FileName: "book.azw3", Size: int64(len(payload)), FileType: "application/octet-stream"}}}
	b, _ := json.Marshal(preq)
	resp, err := client.Post(ts.URL+"/api/localsend/v2/prepare-upload", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("prepare HTTP %d: %s", resp.StatusCode, body)
	}
	var prep PrepareUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&prep); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()

	u := ts.URL + "/api/localsend/v2/upload?sessionId=" + prep.SessionID + "&fileId=f1&token=" + prep.Files["f1"]
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(payload))
	// reqwest Body::wrap_stream does not advertise a fixed Content-Length;
	// forcing -1 makes Go use chunked HTTP/1.1 and exercises that path.
	req.ContentLength = -1
	up, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Body.Close()
	if up.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(up.Body)
		t.Fatalf("upload HTTP %d: %s", up.StatusCode, body)
	}
	got, err := os.ReadFile(filepath.Join(cfg.ReceiveDir, "book.azw3"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("chunked upload payload mismatch")
	}
}

func TestHTTPCompatPrepareAndUpload(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t, root)
	cfg.Encryption = false
	serverID, err := LoadOrCreateIdentity(filepath.Join(root, "server-id"))
	if err != nil {
		t.Fatal(err)
	}
	clientID, err := LoadOrCreateIdentity(filepath.Join(root, "client-id"))
	if err != nil {
		t.Fatal(err)
	}
	state := NewStateStore(root)
	s := NewServer(root, cfg, serverID, state, log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/localsend/v2/prepare-upload", s.handlePrepareUpload)
	mux.HandleFunc("/api/localsend/v2/upload", s.handleUpload)
	ts := httptest.NewServer(s.requestLogMiddleware(s.limitBodyMiddleware(mux)))
	defer ts.Close()

	payload := []byte("windows-to-voyage-http-compat")
	info := DeviceInfo{Alias: "Windows", Version: "2.2", DeviceModel: "Windows", DeviceType: "desktop", Fingerprint: clientID.Fingerprint, Port: 53317, Protocol: "https"}
	preq := PrepareUploadRequest{Info: info, Files: map[string]FileInfo{"f1": {ID: "f1", FileName: "compat.azw3", Size: int64(len(payload)), FileType: "application/octet-stream"}}}
	b, _ := json.Marshal(preq)
	resp, err := http.Post(ts.URL+"/api/localsend/v2/prepare-upload", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("prepare HTTP %d: %s", resp.StatusCode, body)
	}
	var prep PrepareUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&prep); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()

	u := ts.URL + "/api/localsend/v2/upload?sessionId=" + prep.SessionID + "&fileId=f1&token=" + prep.Files["f1"]
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(payload))
	req.ContentLength = -1
	up, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	up.Body.Close()
	if up.StatusCode != http.StatusOK {
		t.Fatalf("upload HTTP %d", up.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(cfg.ReceiveDir, "compat.azw3"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("HTTP compatibility payload mismatch")
	}
}

func TestPrepareUploadIgnoresUnknownJSONFields(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t, root)
	cfg.Encryption = false
	id, err := LoadOrCreateIdentity(filepath.Join(root, "id"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(root, cfg, id, NewStateStore(root), log.New(io.Discard, "", 0))
	reqJSON := `{
		"info":{"alias":"Windows","version":"2.2","deviceModel":"Windows","deviceType":"desktop","fingerprint":"ABC","port":53317,"protocol":"http","download":false,"futureInfoField":"ok"},
		"files":{"f1":{"id":"f1","fileName":"future.epub","size":4,"fileType":"application/epub+zip","futureFileField":{"x":1}}},
		"futureRequestField":true
	}`
	r := httptest.NewRequest(http.MethodPost, "/api/localsend/v2/prepare-upload", strings.NewReader(reqJSON))
	w := httptest.NewRecorder()
	s.handlePrepareUpload(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("prepare with unknown fields HTTP %d: %s", w.Code, w.Body.String())
	}
}

func TestDefaultCompatibilityModeUsesHTTP(t *testing.T) {
	cfg := DefaultConfig("ignored")
	if cfg.Encryption {
		t.Fatal("default Voyage compatibility mode must use HTTP")
	}
}

func TestHTTPFingerprintStableAndSeparateFromTLS(t *testing.T) {
	root := t.TempDir()
	id, err := LoadOrCreateIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	fp1, err := LoadOrCreateHTTPFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := LoadOrCreateHTTPFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatal("HTTP fingerprint changed across reload")
	}
	if strings.EqualFold(fp1, id.Fingerprint) {
		t.Fatal("HTTP compatibility identity must be separate from TLS certificate identity")
	}
}

func TestFirewallLeaseUsesIsolatedChain(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "iptables.args")
	fake := filepath.Join(root, "iptables")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALSEND_IPTABLES", fake)
	fw, err := OpenFirewall("wlan0", 53317, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		"-N LSKUAL",
		"-A LSKUAL -p tcp --dport 53317 -j ACCEPT",
		"-A LSKUAL -p udp --dport 53317 -j ACCEPT",
		"-I INPUT 1 -i wlan0 -j LSKUAL",
		"-D INPUT -i wlan0 -j LSKUAL",
		"-X LSKUAL",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("iptables log missing %q:\n%s", want, got)
		}
	}
}

func startCompatHTTPTestServer(t *testing.T) (*Server, Config, string, func()) {
	t.Helper()
	root := t.TempDir()
	cfg := testConfig(t, root)
	cfg.Encryption = false
	id, err := LoadOrCreateIdentity(filepath.Join(root, "id"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(root, cfg, id, NewStateStore(root), log.New(io.Discard, "", 0))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/localsend/v2/prepare-upload", s.handlePrepareUpload)
	mux.HandleFunc("/api/localsend/v2/upload", s.handleUpload)
	mux.HandleFunc("/api/localsend/v2/cancel", s.handleCancel)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: s.requestLogMiddleware(s.limitBodyMiddleware(mux))}
	go func() { _ = hs.Serve(newUploadCompatListener(ln, s)) }()
	cleanup := func() {
		_ = hs.Close()
		_ = ln.Close()
	}
	return s, cfg, "http://" + ln.Addr().String(), cleanup
}

func prepareCompatFile(t *testing.T, baseURL, model, version, name string, payload []byte, sha bool) PrepareUploadResponse {
	t.Helper()
	fi := FileInfo{ID: "f1", FileName: name, Size: int64(len(payload)), FileType: "application/epub+zip"}
	if sha {
		sum := sha256.Sum256(payload)
		h := hex.EncodeToString(sum[:])
		fi.SHA256 = &h
	}
	info := DeviceInfo{Alias: model + " sender", Version: version, DeviceModel: model, DeviceType: "desktop", Fingerprint: strings.Repeat("A", 64), Port: 53317, Protocol: "https"}
	preq := PrepareUploadRequest{Info: info, Files: map[string]FileInfo{"f1": fi}}
	b, _ := json.Marshal(preq)
	resp, err := http.Post(baseURL+"/api/localsend/v2/prepare-upload", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("prepare HTTP %d: %s", resp.StatusCode, body)
	}
	var out PrepareUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func rawUploadStatus(t *testing.T, baseURL, target string, headerBody, trailing []byte) string {
	t.Helper()
	addr := strings.TrimPrefix(baseURL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req := "POST " + target + " HTTP/1.1\r\nHost: " + addr + "\r\nContent-Type: application/octet-stream\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write(append([]byte(req), headerBody...)); err != nil {
		t.Fatal(err)
	}
	if len(trailing) > 0 {
		if _, err := conn.Write(trailing); err != nil {
			t.Fatal(err)
		}
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(line)
}

func rawUploadStatusNoLength(t *testing.T, baseURL, target string, body []byte) string {
	t.Helper()
	addr := strings.TrimPrefix(baseURL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	// This is the exact framing class observed on the real macOS sender after
	// Go parsed it: no Content-Length and no Transfer-Encoding, followed by
	// streaming bytes. v0.1.6 tests accidentally used an explicit CL: 0.
	req := "POST " + target + " HTTP/1.1\r\nHost: " + addr + "\r\nContent-Type: application/octet-stream\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write(append([]byte(req), body...)); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(line)
}

func TestMacV22MissingLengthAndMissingTEChunkedIsNormalized(t *testing.T) {
	_, cfg, baseURL, cleanup := startCompatHTTPTestServer(t)
	defer cleanup()
	payload := []byte(strings.Repeat("MAC-NO-CL-CHUNKED-", 4096))
	prep := prepareCompatFile(t, baseURL, "macOS", "2.2", "mac-no-cl.epub", payload, true)
	target := "/api/localsend/v2/upload?sessionId=" + prep.SessionID + "&fileId=f1&token=" + prep.Files["f1"]
	chunked := []byte(fmt.Sprintf("%x\r\n", len(payload)))
	chunked = append(chunked, payload...)
	chunked = append(chunked, []byte("\r\n0\r\n\r\n")...)
	status := rawUploadStatusNoLength(t, baseURL, target, chunked)
	if !strings.Contains(status, " 200 ") {
		t.Fatalf("Mac no-CL chunked status: %s", status)
	}
	got, err := os.ReadFile(filepath.Join(cfg.ReceiveDir, "mac-no-cl.epub"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("Mac no-CL chunked payload mismatch: err=%v", err)
	}
}

func TestMacV22MissingLengthAndMissingTERawBodyIsNormalized(t *testing.T) {
	_, cfg, baseURL, cleanup := startCompatHTTPTestServer(t)
	defer cleanup()
	payload := append([]byte("PK\x03\x04"), []byte(strings.Repeat("MAC-NO-CL-RAW-", 4096))...)
	prep := prepareCompatFile(t, baseURL, "macOS", "2.2", "mac-no-cl-raw.epub", payload, true)
	target := "/api/localsend/v2/upload?sessionId=" + prep.SessionID + "&fileId=f1&token=" + prep.Files["f1"]
	status := rawUploadStatusNoLength(t, baseURL, target, payload)
	if !strings.Contains(status, " 200 ") {
		t.Fatalf("Mac no-CL raw status: %s", status)
	}
	got, err := os.ReadFile(filepath.Join(cfg.ReceiveDir, "mac-no-cl-raw.epub"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("Mac no-CL raw payload mismatch: err=%v", err)
	}
}

func TestDualPlatformWindowsFixedLengthStillUsesBaselinePath(t *testing.T) {
	_, cfg, baseURL, cleanup := startCompatHTTPTestServer(t)
	defer cleanup()
	payload := []byte(strings.Repeat("WINDOWS-BASELINE-", 4096))
	prep := prepareCompatFile(t, baseURL, "Windows", "2.1", "windows.epub", payload, true)
	u := baseURL + "/api/localsend/v2/upload?sessionId=" + prep.SessionID + "&fileId=f1&token=" + prep.Files["f1"]
	resp, err := http.Post(u, "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Windows upload HTTP %d", resp.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(cfg.ReceiveDir, "windows.epub"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("Windows payload mismatch: err=%v", err)
	}
}

func TestMacV22MissingTEChunkedIsNormalizedBeforeNetHTTP(t *testing.T) {
	_, cfg, baseURL, cleanup := startCompatHTTPTestServer(t)
	defer cleanup()
	payload := []byte(strings.Repeat("MAC-CHUNKED-PAYLOAD-", 4096))
	prep := prepareCompatFile(t, baseURL, "macOS", "2.2", "mac-chunked.epub", payload, true)
	target := "/api/localsend/v2/upload?sessionId=" + prep.SessionID + "&fileId=f1&token=" + prep.Files["f1"]
	chunked := []byte(fmt.Sprintf("%x\r\n", len(payload)))
	chunked = append(chunked, payload...)
	chunked = append(chunked, []byte("\r\n0\r\n\r\n")...)
	status := rawUploadStatus(t, baseURL, target, chunked, nil)
	if !strings.Contains(status, " 200 ") {
		t.Fatalf("Mac chunked status: %s", status)
	}
	got, err := os.ReadFile(filepath.Join(cfg.ReceiveDir, "mac-chunked.epub"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("Mac chunked payload mismatch: err=%v", err)
	}
}

func TestMacV22ZeroLengthHeaderRawBodyIsNormalized(t *testing.T) {
	_, cfg, baseURL, cleanup := startCompatHTTPTestServer(t)
	defer cleanup()
	payload := append([]byte("PK\x03\x04"), []byte(strings.Repeat("MAC-RAW-PAYLOAD-", 4096))...)
	prep := prepareCompatFile(t, baseURL, "macOS", "2.2", "mac-raw.epub", payload, true)
	target := "/api/localsend/v2/upload?sessionId=" + prep.SessionID + "&fileId=f1&token=" + prep.Files["f1"]
	status := rawUploadStatus(t, baseURL, target, payload, nil)
	if !strings.Contains(status, " 200 ") {
		t.Fatalf("Mac raw status: %s", status)
	}
	got, err := os.ReadFile(filepath.Join(cfg.ReceiveDir, "mac-raw.epub"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("Mac raw payload mismatch: err=%v", err)
	}
}

func TestPrepareReplayReturnsSameSessionAndTokens(t *testing.T) {
	_, _, baseURL, cleanup := startCompatHTTPTestServer(t)
	defer cleanup()
	payload := []byte("retry prepare")
	first := prepareCompatFile(t, baseURL, "Windows", "2.1", "retry.epub", payload, false)
	second := prepareCompatFile(t, baseURL, "Windows", "2.1", "retry.epub", payload, false)
	if first.SessionID != second.SessionID || first.Files["f1"] != second.Files["f1"] {
		t.Fatalf("prepare replay changed session/token: first=%+v second=%+v", first, second)
	}
}
