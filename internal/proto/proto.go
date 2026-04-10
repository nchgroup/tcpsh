// Package proto implements the framing and encryption layer used between
// tcpsh -server and -client.
//
// Wire format (per frame):
//
//	[4-byte big-endian length L][12-byte random nonce][L-28 bytes ciphertext + 16-byte AEAD tag]
//
// Key derivation:
//
//	key = SHA-256(token)   (token is a 32-char [A-Za-z0-9] string)
package proto

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	nonceSize     = chacha20poly1305.NonceSize // 12
	tagSize       = 16
	lenFieldSize  = 4
	tokenLen      = 32
	tokenCharset  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	maxFrameBytes = 4 * 1024 * 1024 // 4 MiB sanity cap

	// Frame type bytes (first byte of plaintext payload).
	FrameResponse     = byte('R') // direct response to a command
	FrameEvent        = byte('E') // unsolicited push event
	FrameSessionStart = byte('S') // server→client: entering session mode; payload = "port:idx:remote"
)

// GenerateToken returns a cryptographically random 32-character token using
// only [A-Za-z0-9] characters.
func GenerateToken() (string, error) {
	charsetLen := big.NewInt(int64(len(tokenCharset)))
	buf := make([]byte, tokenLen)
	for i := range buf {
		n, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("proto: generate token: %w", err)
		}
		buf[i] = tokenCharset[n.Int64()]
	}
	return string(buf), nil
}

// ValidateToken returns an error if token is not a valid 32-char [A-Za-z0-9]
// string.
func ValidateToken(token string) error {
	if len(token) != tokenLen {
		return fmt.Errorf("must be exactly %d characters, got %d", tokenLen, len(token))
	}
	for _, c := range token {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return fmt.Errorf("invalid character %q — only [A-Za-z0-9] allowed", c)
		}
	}
	return nil
}

// TokenToKey derives a 32-byte ChaCha20-Poly1305 key from a token string via
// SHA-256.
func TokenToKey(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}

// WriteFrame encrypts plaintext with the given key and writes a framed message
// to w.  A fresh random nonce is generated for every frame.
func WriteFrame(w io.Writer, key [32]byte, plaintext []byte) error {
	aead, err := chacha20poly1305.New(key[:])
	if err != nil {
		return fmt.Errorf("proto: new cipher: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("proto: nonce: %w", err)
	}

	ciphertext := aead.Seal(nil, nonce, plaintext, nil)

	// Frame length = nonce + ciphertext (which already includes the 16-byte tag).
	frameLen := uint32(nonceSize + len(ciphertext))
	var hdr [lenFieldSize]byte
	binary.BigEndian.PutUint32(hdr[:], frameLen)

	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("proto: write header: %w", err)
	}
	if _, err := w.Write(nonce); err != nil {
		return fmt.Errorf("proto: write nonce: %w", err)
	}
	if _, err := w.Write(ciphertext); err != nil {
		return fmt.Errorf("proto: write ciphertext: %w", err)
	}
	return nil
}

// ReadFrame reads exactly one framed message from r, decrypts it, and returns
// the plaintext.  Returns an error if decryption fails (wrong key or tampered
// data) or if the frame is malformed.
func ReadFrame(r io.Reader, key [32]byte) ([]byte, error) {
	var hdr [lenFieldSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("proto: read header: %w", err)
	}
	frameLen := binary.BigEndian.Uint32(hdr[:])
	if frameLen < uint32(nonceSize+tagSize) {
		return nil, errors.New("proto: frame too short")
	}
	if frameLen > maxFrameBytes {
		return nil, fmt.Errorf("proto: frame too large (%d bytes)", frameLen)
	}

	frame := make([]byte, frameLen)
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, fmt.Errorf("proto: read frame: %w", err)
	}

	nonce := frame[:nonceSize]
	ciphertext := frame[nonceSize:]

	aead, err := chacha20poly1305.New(key[:])
	if err != nil {
		return nil, fmt.Errorf("proto: new cipher: %w", err)
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("proto: decrypt: %w", err)
	}
	return plaintext, nil
}

// SendHandshake performs the client side of the authentication handshake:
// sends HELLO and waits for READY.
func SendHandshake(w io.Writer, r io.Reader, key [32]byte) error {
	if err := WriteFrame(w, key, []byte("HELLO")); err != nil {
		return fmt.Errorf("proto: handshake send: %w", err)
	}
	reply, err := ReadFrame(r, key)
	if err != nil {
		return fmt.Errorf("proto: handshake recv: %w", err)
	}
	if string(reply) != "READY" {
		return fmt.Errorf("proto: unexpected handshake reply %q", string(reply))
	}
	return nil
}

// ReceiveHandshake performs the server side of the authentication handshake:
// waits for HELLO, replies READY.  Returns an error if the client supplied the
// wrong token (AEAD decryption will fail).
func ReceiveHandshake(w io.Writer, r io.Reader, key [32]byte) error {
	msg, err := ReadFrame(r, key)
	if err != nil {
		return fmt.Errorf("proto: handshake recv: %w", err)
	}
	if string(msg) != "HELLO" {
		return fmt.Errorf("proto: unexpected handshake message %q", string(msg))
	}
	if err := WriteFrame(w, key, []byte("READY")); err != nil {
		return fmt.Errorf("proto: handshake reply: %w", err)
	}
	return nil
}

// WriteTypedFrame prepends a type byte to payload and encrypts it as a frame.
func WriteTypedFrame(w io.Writer, key [32]byte, typ byte, payload []byte) error {
	plain := make([]byte, 1+len(payload))
	plain[0] = typ
	copy(plain[1:], payload)
	return WriteFrame(w, key, plain)
}

// ReadTypedFrame reads a frame and returns its type byte and the remaining payload.
func ReadTypedFrame(r io.Reader, key [32]byte) (byte, []byte, error) {
	plain, err := ReadFrame(r, key)
	if err != nil {
		return 0, nil, err
	}
	if len(plain) == 0 {
		return 0, nil, errors.New("proto: empty typed frame")
	}
	return plain[0], plain[1:], nil
}

// NewBufReader wraps a net.Conn (or any io.Reader) in a bufio.Reader for
// efficient frame reads.
func NewBufReader(r io.Reader) *bufio.Reader {
	return bufio.NewReaderSize(r, 64*1024)
}
