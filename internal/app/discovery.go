package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func (s *Server) runDiscovery(ctx context.Context) error {
	group := &net.UDPAddr{IP: net.ParseIP(MulticastIP), Port: s.cfg.Port}
	var iface *net.Interface
	if s.cfg.Interface != "" {
		if i, err := net.InterfaceByName(s.cfg.Interface); err == nil {
			iface = i
		}
	}
	conn, err := net.ListenMulticastUDP("udp4", iface, group)
	if err != nil {
		return fmt.Errorf("multicast listen: %w", err)
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(64 << 10)

	announce := func() {
		b, _ := json.Marshal(s.ownInfo())
		_, err := conn.WriteToUDP(b, group)
		if err != nil {
			s.logger.Printf("multicast announce failed: %v", err)
		}
	}
	announce()
	ticker := time.NewTicker(time.Duration(s.cfg.AnnounceSeconds) * time.Second)
	defer ticker.Stop()

	buf := make([]byte, 64<<10)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, remote, err := conn.ReadFromUDP(buf)
		if err == nil {
			var info DeviceInfo
			if json.Unmarshal(buf[:n], &info) == nil && info.Fingerprint != "" && !strings.EqualFold(info.Fingerprint, s.ownInfo().Fingerprint) {
				p := Peer{DeviceInfo: info, IP: remote.IP.String(), LastSeen: time.Now().UTC()}
				s.state.SavePeer(p, s.cfg.MaxPeers)
				// Protocol v2.2 multicast packets are announcements only. In the
				// Voyage compatibility setup our server intentionally advertises
				// HTTP while desktop peers commonly advertise HTTPS. Answering an
				// HTTPS announcement from an HTTP identity would require presenting
				// the TLS certificate fingerprint instead of the advertised HTTP
				// fingerprint, creating two identities for the same Kindle on the
				// desktop. It is also unnecessary: peers answer our own multicast
				// announcements by POSTing /register to this server, and we already
				// retain the peer endpoint from this announcement for outbound send.
				if s.cfg.Encryption || !strings.EqualFold(p.Protocol, "https") {
					go s.registerWithPeer(p)
				}
			}
		} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			announce()
		case <-s.announce:
			announce()
		default:
		}
	}
}

func (s *Server) registerWithPeer(peer Peer) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := HTTPClientForPeer(s.identity, peer, 5*time.Second)
	b, _ := json.Marshal(s.ownInfo())
	url := fmt.Sprintf("%s://%s:%d/api/localsend/v2/register", peer.Protocol, peer.IP, peer.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		s.logger.Printf("register with %s failed: %v", peer.Alias, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return
	}
	var returned DeviceResponse
	if json.NewDecoder(resp.Body).Decode(&returned) == nil && returned.Fingerprint != "" {
		// /register responses intentionally do not contain port/protocol. Keep
		// the transport endpoint from the multicast announcement/request.
		merged := peer.DeviceInfo
		merged.Alias = returned.Alias
		merged.Version = returned.Version
		merged.DeviceModel = returned.DeviceModel
		merged.DeviceType = returned.DeviceType
		merged.Fingerprint = returned.Fingerprint
		merged.Download = returned.Download
		s.state.SavePeer(Peer{DeviceInfo: merged, IP: peer.IP, LastSeen: time.Now().UTC()}, s.cfg.MaxPeers)
	}
}
