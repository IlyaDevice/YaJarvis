package main

// Минимальный клиент ADB по сети (без adb.exe): подключение, shell-команды и отправка файлов (sync).
// Протокол: https://android.googlesource.com/platform/packages/modules/adb/+/refs/heads/main/protocol.txt

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	aCNXN = 0x4e584e43
	aAUTH = 0x48545541
	aOPEN = 0x4e45504f
	aOKAY = 0x59414b4f
	aCLSE = 0x45534c43
	aWRTE = 0x45545257

	adbVersion = 0x01000000
	maxPayload = 256 * 1024
)

type adbMsg struct {
	cmd, arg0, arg1 uint32
	data            []byte
}

type ADB struct {
	conn    net.Conn
	maxData int
	nextID  uint32
	Banner  string
}

func adbKey() (*rsa.PrivateKey, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	path := filepath.Join(dir, "JarvisSetup", "adbkey")
	if b, err := os.ReadFile(path); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if k, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err == nil {
				return k, nil
			}
		}
	}
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}), 0o600)
	return k, nil
}

// androidPubKey — открытый ключ в формате adbd (mincrypt RSAPublicKey, base64).
func androidPubKey(k *rsa.PublicKey) string {
	const words = 64
	n := k.N
	r32 := new(big.Int).Lsh(big.NewInt(1), 32)
	n0inv := new(big.Int).ModInverse(new(big.Int).Mod(n, r32), r32)
	n0inv.Sub(r32, n0inv)
	rr := new(big.Int).Lsh(big.NewInt(1), 2048*2)
	rr.Mod(rr, n)
	var buf bytes.Buffer
	w := func(v uint32) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	w(words)
	w(uint32(n0inv.Uint64()))
	le := func(x *big.Int) {
		b := x.FillBytes(make([]byte, words*4)) // big endian
		for i := words - 1; i >= 0; i-- {
			w(binary.BigEndian.Uint32(b[i*4:]))
		}
	}
	le(n)
	le(rr)
	w(uint32(k.E))
	host, _ := os.Hostname()
	return base64.StdEncoding.EncodeToString(buf.Bytes()) + " jarvis-setup@" + host
}

func DialADB(addr string, timeout time.Duration) (*ADB, error) {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	a := &ADB{conn: c, maxData: 4096, nextID: 1}
	_ = c.SetDeadline(time.Now().Add(timeout + 5*time.Second))
	if err := a.send(aCNXN, adbVersion, maxPayload, []byte("host::\x00")); err != nil {
		c.Close()
		return nil, err
	}
	key, _ := adbKey()
	sentSig := false
	for {
		m, err := a.read()
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("колонка не ответила по ADB: %w", err)
		}
		switch m.cmd {
		case aCNXN:
			a.maxData = int(m.arg1)
			if a.maxData <= 0 || a.maxData > maxPayload {
				a.maxData = 4096
			}
			a.Banner = strings.TrimRight(string(m.data), "\x00")
			_ = c.SetDeadline(time.Time{})
			return a, nil
		case aAUTH:
			if key == nil {
				c.Close()
				return nil, errors.New("колонка требует ключ ADB, а создать его не получилось")
			}
			if m.arg0 == 1 && !sentSig { // TOKEN
				sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA1, m.data)
				if err == nil {
					sentSig = true
					_ = a.send(aAUTH, 2, 0, sig)
					continue
				}
			}
			_ = a.send(aAUTH, 3, 0, append([]byte(androidPubKey(&key.PublicKey)), 0))
			_ = c.SetDeadline(time.Now().Add(30 * time.Second)) // на колонке нет экрана, но вдруг
		default:
			// мусор до рукопожатия пропускаем
		}
	}
}

func (a *ADB) Close() { a.conn.Close() }

func (a *ADB) send(cmd, arg0, arg1 uint32, data []byte) error {
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h[0:], cmd)
	binary.LittleEndian.PutUint32(h[4:], arg0)
	binary.LittleEndian.PutUint32(h[8:], arg1)
	binary.LittleEndian.PutUint32(h[12:], uint32(len(data)))
	var sum uint32
	for _, b := range data {
		sum += uint32(b)
	}
	binary.LittleEndian.PutUint32(h[16:], sum)
	binary.LittleEndian.PutUint32(h[20:], cmd^0xffffffff)
	if _, err := a.conn.Write(h); err != nil {
		return err
	}
	if len(data) > 0 {
		_, err := a.conn.Write(data)
		return err
	}
	return nil
}

func (a *ADB) read() (adbMsg, error) {
	h := make([]byte, 24)
	if _, err := io.ReadFull(a.conn, h); err != nil {
		return adbMsg{}, err
	}
	m := adbMsg{cmd: binary.LittleEndian.Uint32(h[0:]), arg0: binary.LittleEndian.Uint32(h[4:]), arg1: binary.LittleEndian.Uint32(h[8:])}
	n := binary.LittleEndian.Uint32(h[12:])
	if binary.LittleEndian.Uint32(h[20:]) != m.cmd^0xffffffff {
		return m, errors.New("ADB: повреждённый пакет")
	}
	if n > 16<<20 {
		return m, errors.New("ADB: слишком большой пакет")
	}
	if n > 0 {
		m.data = make([]byte, n)
		if _, err := io.ReadFull(a.conn, m.data); err != nil {
			return m, err
		}
	}
	return m, nil
}

type adbStream struct {
	a               *ADB
	local, remote   uint32
	closed          bool
}

func (a *ADB) open(service string, timeout time.Duration) (*adbStream, error) {
	_ = a.conn.SetDeadline(time.Now().Add(timeout))
	id := a.nextID
	a.nextID++
	if err := a.send(aOPEN, id, 0, append([]byte(service), 0)); err != nil {
		return nil, err
	}
	for {
		m, err := a.read()
		if err != nil {
			return nil, err
		}
		if m.arg1 != id {
			continue // хвосты прошлых потоков
		}
		switch m.cmd {
		case aOKAY:
			return &adbStream{a: a, local: id, remote: m.arg0}, nil
		case aCLSE:
			return nil, fmt.Errorf("колонка отказала в %q", service)
		}
	}
}

// recv — следующий кусок данных потока; nil, io.EOF — поток закрыт.
func (s *adbStream) recv() ([]byte, error) {
	for {
		m, err := s.a.read()
		if err != nil {
			return nil, err
		}
		if m.arg1 != s.local {
			continue
		}
		switch m.cmd {
		case aWRTE:
			if err := s.a.send(aOKAY, s.local, s.remote, nil); err != nil {
				return nil, err
			}
			return m.data, nil
		case aCLSE:
			if !s.closed {
				_ = s.a.send(aCLSE, s.local, s.remote, nil)
				s.closed = true
			}
			return nil, io.EOF
		}
	}
}

// write — отправить и дождаться подтверждения (ADB пускает по одному пакету).
func (s *adbStream) write(b []byte) error {
	for len(b) > 0 {
		n := min(len(b), s.a.maxData)
		if err := s.a.send(aWRTE, s.local, s.remote, b[:n]); err != nil {
			return err
		}
		for {
			m, err := s.a.read()
			if err != nil {
				return err
			}
			if m.arg1 != s.local {
				continue
			}
			if m.cmd == aOKAY {
				break
			}
			if m.cmd == aCLSE {
				return errors.New("колонка закрыла поток")
			}
			if m.cmd == aWRTE { // ответ sync может прийти сразу — вернём его через pending
				_ = s.a.send(aOKAY, s.local, s.remote, nil)
				return fmt.Errorf("sync: %s", syncError(m.data))
			}
		}
		b = b[n:]
	}
	return nil
}

func (s *adbStream) close() {
	if !s.closed {
		_ = s.a.send(aCLSE, s.local, s.remote, nil)
		s.closed = true
	}
}

// Shell — выполнить команду, вернуть вывод и код выхода.
func (a *ADB) Shell(cmd string, timeout time.Duration) (string, int, error) {
	const marker = "__JARVIS_RC="
	s, err := a.open("shell:"+cmd+"; echo "+marker+"$?", timeout)
	if err != nil {
		return "", -1, err
	}
	var out bytes.Buffer
	for {
		b, err := s.recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out.String(), -1, err
		}
		out.Write(b)
	}
	_ = a.conn.SetDeadline(time.Time{})
	text := strings.ReplaceAll(out.String(), "\r\n", "\n")
	i := strings.LastIndex(text, marker)
	if i < 0 {
		return text, -1, nil
	}
	var rc int
	fmt.Sscanf(text[i+len(marker):], "%d", &rc)
	return strings.TrimRight(text[:i], "\n"), rc, nil
}

func syncError(b []byte) string {
	if len(b) >= 8 && string(b[:4]) == "FAIL" {
		return string(b[8:])
	}
	return string(b)
}

// Push — записать файл на колонку (протокол sync).
func (a *ADB) Push(data []byte, remote string, mode uint32, progress func(done, total int)) error {
	s, err := a.open("sync:", 30*time.Second)
	if err != nil {
		return err
	}
	defer s.close()
	pkt := func(id string, n uint32, payload []byte) []byte {
		b := make([]byte, 8+len(payload))
		copy(b, id)
		binary.LittleEndian.PutUint32(b[4:], n)
		copy(b[8:], payload)
		return b
	}
	spec := fmt.Sprintf("%s,%d", remote, 0o100000|mode) // обычный файл + права
	if err := s.write(pkt("SEND", uint32(len(spec)), []byte(spec))); err != nil {
		return err
	}
	const chunk = 64 * 1024
	for off := 0; off < len(data); off += chunk {
		end := min(off+chunk, len(data))
		_ = a.conn.SetDeadline(time.Now().Add(60 * time.Second))
		if err := s.write(pkt("DATA", uint32(end-off), data[off:end])); err != nil {
			return err
		}
		if progress != nil {
			progress(end, len(data))
		}
	}
	if err := s.write(pkt("DONE", uint32(time.Now().Unix()), nil)); err != nil {
		return err
	}
	resp, err := s.recv()
	if err != nil {
		return err
	}
	if len(resp) >= 4 && string(resp[:4]) == "OKAY" {
		_ = s.write(pkt("QUIT", 0, nil))
		_ = a.conn.SetDeadline(time.Time{})
		return nil
	}
	return fmt.Errorf("колонка не приняла файл: %s", syncError(resp))
}
