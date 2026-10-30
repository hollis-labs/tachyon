package chatworkspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var (
	ErrTooLarge = errors.New("file is larger than 25 MiB")
	ErrType     = errors.New("this file type is not allowed")
)

// blockedExt are never accepted, whatever their bytes sniff as.
var blockedExt = map[string]bool{".html": true, ".htm": true, ".xhtml": true, ".js": true, ".mjs": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".sh": true, ".bash": true, ".bat": true, ".cmd": true, ".com": true, ".msi": true, ".jar": true, ".app": true, ".ps1": true, ".scr": true, ".apk": true}

// CleanName reduces a client-supplied name to a safe base name.
func CleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == ".." || name == "/" {
		name = ""
	}
	if len(name) > 200 {
		name = strings.ToValidUTF8(name[:200], "")
	}
	return name
}

// Classify decides what an upload is from its bytes and name. The allow-list is by sniffed
// type: text, JSON, PDF, PNG, JPEG, GIF, WebP, and SVG (download-only). The declared type is
// ignored. It returns the stored mime and the item type (file or image).
func Classify(name string, head []byte, whole bool) (mime, typ string, err error) {
	ext := strings.ToLower(filepath.Ext(name))
	if blockedExt[ext] {
		return "", "", ErrType
	}
	sniff := http.DetectContentType(head)
	base, _, _ := strings.Cut(sniff, ";")
	switch base {
	case "application/pdf":
		return base, "file", nil
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return base, "image", nil
	case "text/plain", "text/xml":
		// Text must be valid UTF-8 (a multi-byte rune may be cut at the end of the sniffed head).
		if !utf8.Valid(head) && (whole || !utf8.Valid(head[:len(head)-min(3, len(head))])) {
			return "", "", ErrType
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return "", "", ErrType
		}
		switch ext {
		case ".svg":
			return "image/svg+xml", "file", nil
		case ".json":
			return "application/json", "file", nil
		case ".md", ".markdown":
			return "text/markdown", "file", nil
		case ".csv":
			return "text/csv", "file", nil
		}
		if base == "text/xml" {
			return "text/xml", "file", nil
		}
		return "text/plain", "file", nil
	}
	return "", "", ErrType
}

// Receive copies r to a temporary file under the thread's directory, hashing as it goes and
// refusing more than MaxFileBytes. It returns the temp path, the sniffed head, size and digest.
func (s *Store) Receive(thread string, r io.Reader) (tmp string, head []byte, size int64, sum string, err error) {
	dir := filepath.Dir(s.Path(thread, "x"))
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return
	}
	tmp = f.Name()
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(tmp)
			tmp = ""
		}
	}()
	h := sha256.New()
	buf := make([]byte, 32<<10)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if size+int64(n) > MaxFileBytes {
				err = ErrTooLarge
				return
			}
			if len(head) < 512 {
				head = append(head, buf[:min(n, 512-len(head))]...)
			}
			size += int64(n)
			h.Write(buf[:n])
			if _, err = f.Write(buf[:n]); err != nil {
				return
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			err = rerr
			return
		}
	}
	if err = f.Chmod(0o600); err != nil {
		return
	}
	sum = hex.EncodeToString(h.Sum(nil))
	return
}

// validJSON reports whether a .json upload really is JSON; checked on the stored file.
func validJSON(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Valid(b)
}
