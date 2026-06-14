// Package wallet provides Ethereum account generation and AES-GCM encryption
// of private keys for the Wallet reconciler (spec 10.3).
package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Account is a generated Ethereum account.
type Account struct {
	// Address is the checksummed (EIP-55) hex address, e.g. 0xAbC...
	Address string
	// PrivateKey is the 32-byte secp256k1 private key.
	PrivateKey []byte
}

// GenerateAccount creates a new secp256k1 key pair and derives its address.
func GenerateAccount() (Account, error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return Account{}, fmt.Errorf("generate key: %w", err)
	}
	return Account{
		Address:    crypto.PubkeyToAddress(key.PublicKey).Hex(),
		PrivateKey: crypto.FromECDSA(key),
	}, nil
}

// NormalizeAddress validates an Ethereum address and returns it in EIP-55
// checksummed form. ok is false if the address is not a valid hex address.
func NormalizeAddress(addr string) (string, bool) {
	if !common.IsHexAddress(addr) {
		return "", false
	}
	return common.HexToAddress(addr).Hex(), true
}

// DeriveKey turns an arbitrary passphrase into a 32-byte AES-256 key.
func DeriveKey(passphrase []byte) []byte {
	h := sha256.Sum256(passphrase)
	return h[:]
}

// Encrypt seals plaintext with AES-256-GCM. The 12-byte nonce is prepended to
// the returned ciphertext.
func Encrypt(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("read nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. Exposed for tests and downstream consumers.
func Decrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(ciphertext) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}
	return plaintext, nil
}
