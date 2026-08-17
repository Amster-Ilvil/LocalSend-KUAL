package app

import "time"

const (
	ProtocolVersion = "2.2"
	DefaultPort     = 53317
	MulticastIP     = "224.0.0.167"
)

type Config struct {
	Alias           string `json:"alias"`
	Port            int    `json:"port"`
	Encryption      bool   `json:"encryption"`
	AutoAccept      bool   `json:"auto_accept"`
	VerifyChecksums bool   `json:"verify_checksums"`
	PIN             string `json:"pin,omitempty"`
	SendPIN         string `json:"send_pin,omitempty"`
	ReceiveDir      string `json:"receive_dir"`
	OutboxDir       string `json:"outbox_dir"`
	Interface       string `json:"interface"`
	AnnounceSeconds int    `json:"announce_seconds"`
	MaxPeers        int    `json:"max_peers"`
}

type DeviceInfo struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Download    bool   `json:"download,omitempty"`
}

type DeviceResponse struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Download    bool   `json:"download,omitempty"`
}

type Peer struct {
	DeviceInfo
	IP       string    `json:"ip"`
	LastSeen time.Time `json:"last_seen"`
}

type FileMetadata struct {
	Modified *string `json:"modified,omitempty"`
	Accessed *string `json:"accessed,omitempty"`
}

type FileInfo struct {
	ID       string        `json:"id"`
	FileName string        `json:"fileName"`
	Size     int64         `json:"size"`
	FileType string        `json:"fileType"`
	SHA256   *string       `json:"sha256,omitempty"`
	Preview  *string       `json:"preview,omitempty"`
	Metadata *FileMetadata `json:"metadata,omitempty"`
}

type PrepareUploadRequest struct {
	Info  DeviceInfo          `json:"info"`
	Files map[string]FileInfo `json:"files"`
}

type PrepareUploadResponse struct {
	SessionID string            `json:"sessionId"`
	Files     map[string]string `json:"files"`
}

type uploadFileState struct {
	Info     FileInfo
	Token    string
	Received bool
	SavedAs  string
}

type uploadSession struct {
	ID        string
	Sender    Peer
	CreatedAt time.Time
	Files     map[string]*uploadFileState
	Cancelled bool
}

type RuntimeStatus struct {
	Running       bool      `json:"running"`
	PID           int       `json:"pid"`
	StartedAt     time.Time `json:"started_at"`
	Protocol      string    `json:"protocol"`
	Port          int       `json:"port"`
	Alias         string    `json:"alias"`
	Fingerprint   string    `json:"fingerprint"`
	ReceiveDir    string    `json:"receive_dir"`
	LastEvent     string    `json:"last_event"`
	LastEventAt   time.Time `json:"last_event_at"`
	ReceivedFiles int       `json:"received_files"`
	ReceivedBytes int64     `json:"received_bytes"`
	PeerCount     int       `json:"peer_count"`
}
