package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func HTTPClientForPeer(id *Identity, peer Peer, timeout time.Duration) *http.Client {
	tr := &http.Transport{}
	if strings.EqualFold(peer.Protocol, "https") {
		clientCert := id.Cert
		tr.ForceAttemptHTTP2 = false
		tr.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{clientCert},
			// LocalSend servers use their own self-signed certificate as the
			// ClientHello/CertificateRequest CA hint. Historical LocalSend
			// certificates can encode the same CN with a byte-different DN, so
			// Go's automatic certificate selection may decide our certificate
			// does not match the advertised CA list and send no certificate at
			// all. Current rustls servers then fail the handshake with
			// "certificate required". Force the LocalSend identity certificate
			// to be offered; the peer verifies the self-signature/fingerprint.
			GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
				return &clientCert, nil
			},
			NextProtos: []string{"http/1.1"},
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return fmt.Errorf("server did not provide certificate")
				}
				if err := verifySelfSignedRaw([][]byte{cs.PeerCertificates[0].Raw}); err != nil {
					return err
				}
				if peer.Fingerprint != "" && !strings.EqualFold(certFingerprint(cs.PeerCertificates[0].Raw), peer.Fingerprint) {
					return fmt.Errorf("server certificate fingerprint mismatch")
				}
				return nil
			},
		}
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func fileType(path string) string {
	if m := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); m != "" {
		if i := strings.IndexByte(m, ';'); i >= 0 {
			return m[:i]
		}
		return m
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}

func (s *Server) SendFiles(ctx context.Context, peer Peer, paths []string, pin string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no files to send")
	}
	files := make(map[string]FileInfo, len(paths))
	idToPath := make(map[string]string, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			continue
		}
		id, _ := randomHex(8)
		files[id] = FileInfo{ID: id, FileName: filepath.Base(p), Size: st.Size(), FileType: fileType(p)}
		idToPath[id] = p
	}
	if len(files) == 0 {
		return fmt.Errorf("no regular files to send")
	}

	requestInfo := s.ownInfo()
	if strings.EqualFold(peer.Protocol, "https") {
		// HTTPS LocalSend receivers prove the sender identity with the mTLS
		// certificate and require the claimed fingerprint to match it. The
		// Kindle may advertise a separate random fingerprint for its plaintext
		// HTTP server, so use the certificate fingerprint only for this TLS
		// request identity.
		requestInfo.Fingerprint = s.identity.Fingerprint
	}
	prepare := PrepareUploadRequest{Info: requestInfo, Files: files}
	body, _ := json.Marshal(prepare)
	base := fmt.Sprintf("%s://%s:%d", peer.Protocol, peer.IP, peer.Port)
	url := base + "/api/localsend/v2/prepare-upload"
	if pin != "" {
		url += "?pin=" + pin
	}
	client := HTTPClientForPeer(s.identity, peer, 30*time.Minute)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("prepare upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("prepare upload rejected: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var prepResp PrepareUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&prepResp); err != nil {
		return err
	}

	for id, token := range prepResp.Files {
		path := idToPath[id]
		if path == "" {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		u := fmt.Sprintf("%s/api/localsend/v2/upload?sessionId=%s&fileId=%s&token=%s", base, prepResp.SessionID, id, token)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, f)
		req.ContentLength = files[id].Size
		upResp, err := client.Do(req)
		f.Close()
		if err != nil {
			return fmt.Errorf("upload %s: %w", filepath.Base(path), err)
		}
		b, _ := io.ReadAll(io.LimitReader(upResp.Body, 4096))
		upResp.Body.Close()
		if upResp.StatusCode < 200 || upResp.StatusCode >= 300 {
			return fmt.Errorf("upload %s rejected: HTTP %d %s", filepath.Base(path), upResp.StatusCode, strings.TrimSpace(string(b)))
		}
	}
	return nil
}

func ListOutbox(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}
