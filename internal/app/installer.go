package app

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	MaxInstallZIPEntries = 20000
	InstallFreeReserve   = int64(32 << 20)
)

type InstallZIPCandidate struct {
	Token string
	Name string
	Path string
	Size int64
	ModTime time.Time
	SHA256 string
}

type pendingInstall struct {
	Token string `json:"token"`
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64 `json:"size"`
	ModUnixNano int64 `json:"mod_unix_nano"`
	SHA256 string `json:"sha256"`
	SelectedAt int64 `json:"selected_at"`
}

type InstallResult struct {
	ArchiveName string
	Files int
	Directories int
	Bytes int64
	Replaced int
	Created int
}

type installEntry struct {
	zf *zip.File
	rel, target string
	dir bool
	mode os.FileMode
	size int64
}

type appliedFile struct {
	rel, target, backup string
	existed bool
	mode os.FileMode
	mtime time.Time
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil { return "", err }
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil { return "", err }
	return hex.EncodeToString(h.Sum(nil)), nil
}

func installCandidateToken(path string, fi os.FileInfo) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%d", filepath.Base(path), fi.Size(), fi.ModTime().UnixNano())
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func ListInstallZIPs(receiveDir string, max int) ([]InstallZIPCandidate, error) {
	if max <= 0 { max = 8 }
	des, err := os.ReadDir(receiveDir)
	if os.IsNotExist(err) { return nil, nil }
	if err != nil { return nil, err }
	var out []InstallZIPCandidate
	for _, de := range des {
		if de.IsDir() || strings.HasPrefix(de.Name(), ".") || !strings.EqualFold(filepath.Ext(de.Name()), ".zip") { continue }
		fi, err := de.Info()
		if err != nil || !fi.Mode().IsRegular() { continue }
		p := filepath.Join(receiveDir, de.Name())
		out = append(out, InstallZIPCandidate{Token: installCandidateToken(p, fi), Name: de.Name(), Path: p, Size: fi.Size(), ModTime: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModTime.Equal(out[j].ModTime) { return out[i].Name < out[j].Name }
		return out[i].ModTime.After(out[j].ModTime)
	})
	if len(out) > max { out = out[:max] }
	return out, nil
}

func FindInstallZIPByToken(receiveDir, token string) (InstallZIPCandidate, error) {
	cs, err := ListInstallZIPs(receiveDir, 1000)
	if err != nil { return InstallZIPCandidate{}, err }
	for _, c := range cs { if c.Token == token { return c, nil } }
	return InstallZIPCandidate{}, errors.New("ZIP not found or changed; refresh the ZIP list")
}

func pendingInstallPath(root string) string { return filepath.Join(root, "state", "pending-install.json") }

func SelectInstallZIP(root, receiveDir, token string) (InstallZIPCandidate, error) {
	c, err := FindInstallZIPByToken(receiveDir, token)
	if err != nil { return c, err }
	c.SHA256, err = sha256File(c.Path)
	if err != nil { return c, err }
	p := pendingInstall{Token: c.Token, Name: c.Name, Path: c.Path, Size: c.Size, ModUnixNano: c.ModTime.UnixNano(), SHA256: c.SHA256, SelectedAt: time.Now().Unix()}
	return c, atomicWriteJSON(pendingInstallPath(root), p, 0o600)
}

func CancelInstallZIP(root string) error {
	err := os.Remove(pendingInstallPath(root))
	if os.IsNotExist(err) { return nil }
	return err
}

func PendingInstallZIP(root, receiveDir string) (InstallZIPCandidate, error) {
	var p pendingInstall
	if err := readJSON(pendingInstallPath(root), &p); err != nil {
		if os.IsNotExist(err) { return InstallZIPCandidate{}, errors.New("no ZIP has been selected") }
		return InstallZIPCandidate{}, err
	}
	c, err := FindInstallZIPByToken(receiveDir, p.Token)
	if err != nil { return c, err }
	if c.Name != p.Name || c.Path != p.Path || c.Size != p.Size || c.ModTime.UnixNano() != p.ModUnixNano { return c, errors.New("selected ZIP changed after selection; refresh and select it again") }
	c.SHA256, err = sha256File(c.Path)
	if err != nil { return c, err }
	if p.SHA256 == "" || !strings.EqualFold(c.SHA256, p.SHA256) { return c, errors.New("selected ZIP content changed after selection; refresh and select it again") }
	return c, nil
}

func processExists(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

func AcquireInstallLock(root string) (func(), error) {
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o700); err != nil { return nil, err }
	path := filepath.Join(state, "install.lock")
	pid := os.Getpid()
	for try := 0; try < 2; try++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			if _, err = fmt.Fprintf(f, "%d\n", pid); err != nil { f.Close(); _ = os.Remove(path); return nil, err }
			if err = f.Close(); err != nil { _ = os.Remove(path); return nil, err }
			return func() { b, e := os.ReadFile(path); if e == nil { owner, _ := strconv.Atoi(strings.TrimSpace(string(b))); if owner == pid { _ = os.Remove(path) } } }, nil
		}
		if !os.IsExist(err) { return nil, err }
		b, _ := os.ReadFile(path); owner, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if owner == pid || processExists(owner) { return nil, fmt.Errorf("ZIP installer already running with pid %d", owner) }
		_ = os.Remove(path)
	}
	return nil, errors.New("could not acquire ZIP installer lock")
}

func protectedInstallPath(rel string) bool {
	n := strings.ToLower(filepath.ToSlash(rel))
	return n == ".localsend-install-backup" || strings.HasPrefix(n, ".localsend-install-backup/") ||
		n == ".localsend-install-stage" || strings.HasPrefix(n, ".localsend-install-stage/") ||
		n == "extensions/localsend/state" || strings.HasPrefix(n, "extensions/localsend/state/") ||
		n == "extensions/localsend/logs" || strings.HasPrefix(n, "extensions/localsend/logs/") ||
		n == "extensions/localsend/config/settings.json"
}

func normalizeInstallName(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, '\x00') { return "", errors.New("empty or NUL ZIP entry") }
	if strings.Contains(name, "\\") { return "", errors.New("backslash paths are not accepted in install ZIPs") }
	if strings.HasPrefix(name, "/") { return "", errors.New("absolute ZIP path is not allowed") }
	name = strings.TrimSuffix(name, "/")
	if name == "" { return "", nil }
	var parts []string
	for _, p := range strings.Split(name, "/") {
		if p == "" || p == "." { continue }
		if p == ".." { return "", errors.New("parent traversal is not allowed") }
		parts = append(parts, p)
	}
	if len(parts) == 0 { return "", nil }
	rel := filepath.Join(parts...)
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) { return "", errors.New("ZIP path escapes destination root") }
	if protectedInstallPath(rel) { return "", fmt.Errorf("ZIP entry targets protected LocalSend runtime data: %s", rel) }
	return rel, nil
}

func safeExistingParents(root, target string) error {
	rel, err := filepath.Rel(root, filepath.Dir(target))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) { return errors.New("target parent escapes install root") }
	cur := root
	if rel == "." { return nil }
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) { return nil }
		if err != nil { return err }
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() { return fmt.Errorf("unsafe install parent: %s", cur) }
	}
	return nil
}

func preflightInstallZIP(zipPath, destRoot string) (*zip.ReadCloser, []installEntry, int64, int64, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil { return nil, nil, 0, 0, err }
	fail := func(e error) (*zip.ReadCloser, []installEntry, int64, int64, error) { _ = zr.Close(); return nil, nil, 0, 0, e }
	if len(zr.File) == 0 { return fail(errors.New("ZIP is empty")) }
	if len(zr.File) > MaxInstallZIPEntries { return fail(fmt.Errorf("ZIP has too many entries: %d", len(zr.File))) }
	root, err := filepath.Abs(destRoot); if err != nil { return fail(err) }
	source, err := filepath.Abs(zipPath); if err != nil { return fail(err) }
	seen := map[string]bool{}
	var entries []installEntry
	var total, maxExisting int64
	for _, zf := range zr.File {
		rel, err := normalizeInstallName(zf.Name); if err != nil { return fail(fmt.Errorf("unsafe ZIP entry %q: %w", zf.Name, err)) }
		if rel == "" { continue }
		mode := zf.Mode()
		dir := strings.HasSuffix(zf.Name, "/") || mode.IsDir()
		if !dir && !mode.IsRegular() { return fail(fmt.Errorf("ZIP entry is not a regular file: %s", rel)) }
		if mode&os.ModeSymlink != 0 { return fail(fmt.Errorf("symbolic links are not allowed: %s", rel)) }
		key := strings.ToLower(filepath.ToSlash(rel))
		if _, ok := seen[key]; ok { return fail(fmt.Errorf("duplicate ZIP target: %s", rel)) }
		seen[key] = dir
		target := filepath.Join(root, rel)
		check, _ := filepath.Rel(root, target)
		if check == ".." || strings.HasPrefix(check, ".."+string(os.PathSeparator)) { return fail(fmt.Errorf("ZIP entry escapes destination: %s", rel)) }
		if target == source { return fail(fmt.Errorf("ZIP would overwrite its own source file: %s", rel)) }
		if err := safeExistingParents(root, target); err != nil { return fail(err) }
		if fi, err := os.Lstat(target); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 { return fail(fmt.Errorf("target is symlink: %s", rel)) }
			if dir != fi.IsDir() { return fail(fmt.Errorf("file/directory conflict: %s", rel)) }
			if !dir && !fi.Mode().IsRegular() { return fail(fmt.Errorf("special target file: %s", rel)) }
			if !dir && fi.Size() > maxExisting { maxExisting = fi.Size() }
		} else if !os.IsNotExist(err) { return fail(err) }
		sz := int64(0)
		if !dir {
			if zf.UncompressedSize64 > uint64(^uint64(0)>>1) { return fail(fmt.Errorf("ZIP entry too large: %s", rel)) }
			sz = int64(zf.UncompressedSize64)
			if total > (1<<63-1)-sz { return fail(errors.New("ZIP uncompressed size overflow")) }
			total += sz
		}
		perm := mode.Perm(); if dir && perm == 0 { perm = 0o755 }; if !dir && perm == 0 { perm = 0o644 }
		entries = append(entries, installEntry{zf: zf, rel: rel, target: target, dir: dir, mode: perm, size: sz})
	}
	if len(entries) == 0 { return fail(errors.New("ZIP contains no installable entries")) }
	for _, e := range entries {
		p := filepath.Dir(e.rel)
		for p != "." {
			if isDir, ok := seen[strings.ToLower(filepath.ToSlash(p))]; ok && !isDir { return fail(fmt.Errorf("ZIP path conflict: %s is a file and parent", p)) }
			n := filepath.Dir(p); if n == p { break }; p = n
		}
	}
	return zr, entries, total, maxExisting, nil
}

func ensureDirs(root, path string, created *[]string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) { return errors.New("directory escapes install root") }
	if rel == "." { return nil }
	cur := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err == nil {
			if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() { return fmt.Errorf("install component is not a directory: %s", cur) }
			continue
		}
		if !os.IsNotExist(err) { return err }
		if err = os.Mkdir(cur, 0o755); err != nil { return err }
		*created = append(*created, cur)
	}
	return nil
}

func copyExact(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src); if err != nil { return err }; defer in.Close()
	if err = os.MkdirAll(filepath.Dir(dst), 0o700); err != nil { return err }
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()); if err != nil { return err }
	ok := false
	defer func(){ _ = out.Close(); if !ok { _ = os.Remove(dst) } }()
	if _, err = io.Copy(out, in); err != nil { return err }
	if err = out.Sync(); err != nil { return err }
	if err = out.Close(); err != nil { return err }
	ok = true
	return nil
}

func writeEntryAtomic(e installEntry) error {
	r, err := e.zf.Open(); if err != nil { return err }; defer r.Close()
	tmp := filepath.Join(filepath.Dir(e.target), fmt.Sprintf(".%s.localsend-install-%d.part", filepath.Base(e.target), os.Getpid()))
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, e.mode.Perm()); if err != nil { return err }
	ok := false
	defer func(){ _ = out.Close(); if !ok { _ = os.Remove(tmp) } }()
	n, err := io.Copy(out, io.LimitReader(r, e.size+1)); if err != nil { return err }
	if n != e.size { return fmt.Errorf("ZIP entry size mismatch for %s: got %d want %d", e.rel, n, e.size) }
	if err = out.Sync(); err != nil { return err }
	if err = out.Close(); err != nil { return err }
	if err = os.Chmod(tmp, e.mode.Perm()); err != nil { return err }
	if err = os.Rename(tmp, e.target); err != nil { return err }
	if !e.zf.Modified.IsZero() { _ = os.Chtimes(e.target, e.zf.Modified, e.zf.Modified) }
	ok = true
	return nil
}

func rollbackInstall(applied []appliedFile, createdDirs []string, backupRoot string, logger *log.Logger) error {
	var errs []string
	for i := len(applied)-1; i >= 0; i-- {
		a := applied[i]
		if a.existed {
			tmp := filepath.Join(filepath.Dir(a.target), fmt.Sprintf(".%s.localsend-rollback-%d.part", filepath.Base(a.target), os.Getpid()))
			_ = os.Remove(tmp)
			if err := copyExact(a.backup, tmp, a.mode); err != nil { errs = append(errs, fmt.Sprintf("restore %s: %v", a.rel, err)); continue }
			if err := os.Rename(tmp, a.target); err != nil { _ = os.Remove(tmp); errs = append(errs, fmt.Sprintf("restore %s: %v", a.rel, err)); continue }
			_ = os.Chmod(a.target, a.mode.Perm()); _ = os.Chtimes(a.target, a.mtime, a.mtime)
		} else if err := os.Remove(a.target); err != nil && !os.IsNotExist(err) { errs = append(errs, fmt.Sprintf("remove %s: %v", a.rel, err)) }
	}
	for i := len(createdDirs)-1; i >= 0; i-- { _ = os.Remove(createdDirs[i]) }
	if len(errs) > 0 { return errors.New(strings.Join(errs, "; ")) }
	_ = os.RemoveAll(backupRoot); _ = os.Remove(filepath.Dir(backupRoot))
	if logger != nil { logger.Printf("ZIP install rollback completed") }
	return nil
}

func InstallZIPToRoot(zipPath, destRoot string, logger *log.Logger) (InstallResult, error) {
	zr, entries, total, maxExisting, err := preflightInstallZIP(zipPath, destRoot)
	if err != nil { return InstallResult{}, err }
	defer zr.Close()
	free, err := availableBytes(destRoot); if err != nil { return InstallResult{}, fmt.Errorf("check free space: %w", err) }
	need := total + maxExisting + InstallFreeReserve
	if need < total || free < need { return InstallResult{}, fmt.Errorf("insufficient free space: free=%d MiB need~=%d MiB", free>>20, need>>20) }
	root, _ := filepath.Abs(destRoot)
	tx, err := randomHex(6); if err != nil { return InstallResult{}, err }
	backupRoot := filepath.Join(root, ".localsend-install-backup", tx)
	if err = os.MkdirAll(backupRoot, 0o700); err != nil { return InstallResult{}, err }
	var createdDirs []string
	var applied []appliedFile
	res := InstallResult{ArchiveName: filepath.Base(zipPath), Bytes: total}
	fail := func(cause error) (InstallResult, error) {
		if logger != nil { logger.Printf("ZIP install failed: %v; starting rollback", cause) }
		if rb := rollbackInstall(applied, createdDirs, backupRoot, logger); rb != nil { return res, fmt.Errorf("install failed: %v; rollback incomplete: %v; backup kept at %s", cause, rb, backupRoot) }
		return res, fmt.Errorf("install failed and was rolled back: %w", cause)
	}
	for _, e := range entries { if e.dir { if err = ensureDirs(root, e.target, &createdDirs); err != nil { return fail(err) }; res.Directories++ } }
	for _, e := range entries {
		if e.dir { continue }
		if err = ensureDirs(root, filepath.Dir(e.target), &createdDirs); err != nil { return fail(err) }
		a := appliedFile{rel:e.rel, target:e.target}
		if fi, e2 := os.Lstat(e.target); e2 == nil {
			if !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 { return fail(fmt.Errorf("target changed after preflight: %s", e.rel)) }
			a.existed=true; a.mode=fi.Mode(); a.mtime=fi.ModTime(); a.backup=filepath.Join(backupRoot,e.rel)
			if err = copyExact(e.target,a.backup,fi.Mode()); err != nil { return fail(fmt.Errorf("backup %s: %w",e.rel,err)) }
			res.Replaced++
		} else if os.IsNotExist(e2) { res.Created++ } else { return fail(e2) }
		if err = writeEntryAtomic(e); err != nil { return fail(fmt.Errorf("write %s: %w", e.rel, err)) }
		applied = append(applied,a); res.Files++
	}
	for _, e := range entries { if e.dir { if fi, e2 := os.Stat(e.target); e2 == nil && fi.IsDir() { _ = os.Chmod(e.target,e.mode.Perm()); if !e.zf.Modified.IsZero(){ _=os.Chtimes(e.target,e.zf.Modified,e.zf.Modified) } } } }
	if err = os.RemoveAll(backupRoot); err != nil && logger != nil { logger.Printf("ZIP install completed but backup cleanup failed: %v",err) }
	_ = os.Remove(filepath.Dir(backupRoot))
	if logger != nil { logger.Printf("ZIP install completed: archive=%q files=%d dirs=%d bytes=%d replaced=%d created=%d",res.ArchiveName,res.Files,res.Directories,res.Bytes,res.Replaced,res.Created) }
	return res,nil
}

func ConfirmInstallZIP(root, receiveDir, destRoot string, logger *log.Logger) (InstallResult, error) {
	c, err := PendingInstallZIP(root,receiveDir); if err != nil { return InstallResult{},err }
	release, err := AcquireInstallLock(root); if err != nil { return InstallResult{},err }; defer release()
	res, err := InstallZIPToRoot(c.Path,destRoot,logger); if err != nil { return res,err }
	_ = CancelInstallZIP(root)
	return res,nil
}

func MarshalInstallCandidates(cs []InstallZIPCandidate) ([]byte,error) {
	type row struct { Token string `json:"token"`; Name string `json:"name"`; Size int64 `json:"size"` }
	rows := make([]row,0,len(cs)); for _,c := range cs { rows=append(rows,row{c.Token,c.Name,c.Size}) }
	return json.Marshal(rows)
}
