package crypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/blake2b"
)

func AesEncrypt(origData, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	origData = PKCS5Padding(origData, blockSize)
	blockMode := cipher.NewCBCEncrypter(block, key[:blockSize])
	crypted := make([]byte, len(origData))
	blockMode.CryptBlocks(crypted, origData)
	return crypted, nil
}

func AesDecrypt(crypted, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	blockSize := block.BlockSize()
	blockMode := cipher.NewCBCDecrypter(block, key[:blockSize])
	origData := make([]byte, len(crypted))
	blockMode.CryptBlocks(origData, crypted)
	origData, err = PKCS5UnPadding(origData)
	return origData, err
}

// PKCS5Padding Completion when the length is insufficient
func PKCS5Padding(ciphertext []byte, blockSize int) []byte {
	padding := blockSize - len(ciphertext)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(ciphertext, padtext...)
}

// PKCS5UnPadding Remove excess
func PKCS5UnPadding(origData []byte) ([]byte, error) {
	length := len(origData)
	if length == 0 {
		return nil, errors.New("empty data")
	}
	unpadding := int(origData[length-1])
	if unpadding == 0 || unpadding > length {
		return nil, errors.New("len error")
	}
	for _, value := range origData[length-unpadding:] {
		if int(value) != unpadding {
			return nil, errors.New("invalid padding")
		}
	}
	return origData[:(length - unpadding)], nil
}

// EncryptBytes AES-GCM
func EncryptBytes(data []byte, keyStr string) ([]byte, error) {
	if keyStr == "" {
		return data, nil
	}
	key := sha256.Sum256([]byte(keyStr))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("io.ReadFull: %w", err)
	}
	ct := gcm.Seal(nil, nonce, data, nil)
	return append(nonce, ct...), nil
}

// DecryptBytes AES-GCM
func DecryptBytes(enc []byte, keyStr string) ([]byte, error) {
	if keyStr == "" {
		return enc, nil
	}
	key := sha256.Sum256([]byte(keyStr))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	ns := gcm.NonceSize()
	if len(enc) < ns+gcm.Overhead() {
		return nil, fmt.Errorf("ciphertext too short: %d", len(enc))
	}
	nonce, ct := enc[:ns], enc[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm.Open: %w", err)
	}
	return pt, nil
}

// ComputeHMAC Get HMAC value
func ComputeHMAC(passwd string, timestamp int64, randomDataPieces ...[]byte) []byte {
	tsBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBuf, uint64(timestamp))
	allPieces := append([][]byte{tsBuf}, randomDataPieces...)
	return GetHMAC(passwd, allPieces...) // 32bit
}

func GetHMAC(passwd string, data ...[]byte) []byte {
	key := []byte(passwd)
	mac := hmac.New(sha256.New, key)
	for _, data := range data {
		mac.Write(data)
	}
	return mac.Sum(nil) // 32bit
}

// Md5 Generate 32-bit MD5 strings
func Md5(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// Blake2b Generate 64-bit BLAKE2b-256 strings
func Blake2b(s string) string {
	hash := blake2b.Sum256([]byte(s))
	return hex.EncodeToString(hash[:])
}

func FNV1a64(parts ...string) string {
	h := fnv.New64a()
	for _, s := range parts {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	sum := h.Sum(nil) // 8 bytes
	return hex.EncodeToString(sum)
}

func GenerateUUID(nameParts ...string) uuid.UUID {
	name := strings.Join(nameParts, "/")
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name))
}

func GetUUID() uuid.UUID {
	u, err := uuid.NewV7()
	if err != nil {
		return uuid.New()
	}
	return u
}

// GetRandomString returns a random base36 string with an optional deterministic prefix from id.
func GetRandomString(l int, id ...int) string {
	str := "0123456789abcdefghijklmnopqrstuvwxyz"
	dictBytes := []byte(str)
	var result []byte

	if len(id) > 0 {
		idMapped := ""
		for id[0] > 0 {
			idMapped = string(str[id[0]%len(str)]) + idMapped
			id[0] /= len(str)
		}

		result = append(result, []byte(idMapped)...)
	}

	remainingLength := l - len(result)
	if remainingLength > 0 {
		for i := 0; i < remainingLength; i++ {
			nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(dictBytes))))
			if err != nil {
				idx := int(time.Now().UnixNano() % int64(len(dictBytes)))
				result = append(result, dictBytes[idx])
				continue
			}
			result = append(result, dictBytes[int(nBig.Int64())])
		}
	}

	return string(result)
}
