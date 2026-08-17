package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type StateStore struct {
	root            string
	mu              sync.Mutex
	peers           map[string]Peer
	status          RuntimeStatus
	lastPeerPersist time.Time
}

func NewStateStore(root string) *StateStore {
	s := &StateStore{root: root, peers: map[string]Peer{}}
	peerPath := filepath.Join(root, "state", "peers.json")
	var peers []Peer
	if readJSON(peerPath, &peers) == nil {
		for _, p := range peers {
			if p.Fingerprint != "" {
				s.peers[strings.ToUpper(p.Fingerprint)] = p
			}
		}
		if st, err := os.Stat(peerPath); err == nil {
			s.lastPeerPersist = st.ModTime()
		}
	}
	_ = readJSON(filepath.Join(root, "state", "status.json"), &s.status)
	return s
}

func peerMateriallyEqual(a, b Peer) bool {
	return strings.EqualFold(a.Fingerprint, b.Fingerprint) &&
		a.Alias == b.Alias && a.Version == b.Version && a.DeviceModel == b.DeviceModel &&
		a.DeviceType == b.DeviceType && a.Port == b.Port && a.Protocol == b.Protocol &&
		a.Download == b.Download && a.IP == b.IP
}

func (s *StateStore) persistPeersLocked(list []Peer) {
	_ = atomicWriteJSON(filepath.Join(s.root, "state", "peers.json"), list, 0o600)
	s.status.PeerCount = len(list)
	_ = atomicWriteJSON(filepath.Join(s.root, "state", "status.json"), s.status, 0o600)
	s.lastPeerPersist = time.Now()
}

// SavePeer updates LastSeen in memory on every sighting, but avoids rewriting
// peers.json every 30 seconds for an unchanged desktop announcement. New peers,
// endpoint changes and metadata changes are persisted immediately.
func (s *StateStore) SavePeer(p Peer, max int) bool {
	if p.Fingerprint == "" || p.IP == "" {
		return false
	}
	p.Fingerprint = strings.ToUpper(p.Fingerprint)
	p.LastSeen = time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	old, existed := s.peers[p.Fingerprint]
	changed := !existed || !peerMateriallyEqual(old, p)
	s.peers[p.Fingerprint] = p
	list := make([]Peer, 0, len(s.peers))
	for _, x := range s.peers {
		list = append(list, x)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LastSeen.After(list[j].LastSeen) })
	if len(list) > max {
		list = list[:max]
		s.peers = make(map[string]Peer, len(list))
		for _, x := range list {
			s.peers[x.Fingerprint] = x
		}
		changed = true
	}
	s.status.PeerCount = len(list)
	if changed || s.lastPeerPersist.IsZero() || time.Since(s.lastPeerPersist) >= 5*time.Minute {
		s.persistPeersLocked(list)
	}
	return changed
}

func (s *StateStore) Peers() []Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]Peer, 0, len(s.peers))
	for _, p := range s.peers {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LastSeen.After(list[j].LastSeen) })
	return list
}

func (s *StateStore) FindPeer(q string) (Peer, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return Peer{}, fmt.Errorf("empty peer selector")
	}
	peers := s.Peers()
	upper := strings.ToUpper(q)
	for _, p := range peers {
		if strings.EqualFold(p.Fingerprint, q) || strings.HasPrefix(strings.ToUpper(p.Fingerprint), upper) || strings.EqualFold(p.Alias, q) || p.IP == q {
			return p, nil
		}
	}
	return Peer{}, fmt.Errorf("peer %q not found; refresh devices first", q)
}

func (s *StateStore) SetStatus(st RuntimeStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = st
	_ = atomicWriteJSON(filepath.Join(s.root, "state", "status.json"), s.status, 0o600)
}

func (s *StateStore) Event(event string, files int, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.LastEvent = event
	s.status.LastEventAt = time.Now().UTC()
	s.status.ReceivedFiles += files
	s.status.ReceivedBytes += bytes
	s.status.PeerCount = len(s.peers)
	_ = atomicWriteJSON(filepath.Join(s.root, "state", "status.json"), s.status, 0o600)
}

func (s *StateStore) MarkStopped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Running = false
	s.status.PID = 0
	s.status.LastEvent = "stopped"
	s.status.LastEventAt = time.Now().UTC()
	_ = atomicWriteJSON(filepath.Join(s.root, "state", "status.json"), s.status, 0o600)
	_ = os.Remove(filepath.Join(s.root, "state", "daemon.pid"))
}

func (s *StateStore) Status() RuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}
