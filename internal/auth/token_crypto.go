package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"

	"github.com/irinery/middlewareAuth/internal/security"
)

type EncryptedCredentialBlob struct {
	Algorithm  string `json:"algorithm"`
	IV         string `json:"iv"`
	AuthTag    string `json:"authTag"`
	Ciphertext string `json:"ciphertext"`
}

func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func EncryptCredential(secret string, credential StoredOAuthCredential) (*EncryptedCredentialBlob, error) {
	plain, err := json.Marshal(credential)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_ENCRYPTION_FAILED", "falha ao serializar credencial", http.StatusInternalServerError, err)
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_ENCRYPTION_FAILED", "falha ao inicializar AES", http.StatusInternalServerError, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_ENCRYPTION_FAILED", "falha ao inicializar GCM", http.StatusInternalServerError, err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, security.Wrap("ERR_TOKEN_ENCRYPTION_FAILED", "falha ao gerar IV", http.StatusInternalServerError, err)
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	tagSize := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagSize]
	tag := sealed[len(sealed)-tagSize:]
	return &EncryptedCredentialBlob{
		Algorithm:  "aes-256-gcm",
		IV:         base64.RawStdEncoding.EncodeToString(nonce),
		AuthTag:    base64.RawStdEncoding.EncodeToString(tag),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}, nil
}

func DecryptCredential(secret string, blob EncryptedCredentialBlob) (*StoredOAuthCredential, error) {
	if blob.Algorithm != "aes-256-gcm" {
		return nil, security.NewError("ERR_TOKEN_DECRYPTION_FAILED", "algoritmo de criptografia nao suportado", http.StatusInternalServerError)
	}
	nonce, err := base64.RawStdEncoding.DecodeString(blob.IV)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_DECRYPTION_FAILED", "IV invalido", http.StatusInternalServerError, err)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(blob.Ciphertext)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_DECRYPTION_FAILED", "ciphertext invalido", http.StatusInternalServerError, err)
	}
	tag, err := base64.RawStdEncoding.DecodeString(blob.AuthTag)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_DECRYPTION_FAILED", "auth tag invalida", http.StatusInternalServerError, err)
	}
	block, err := aes.NewCipher(deriveKey(secret))
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_DECRYPTION_FAILED", "falha ao inicializar AES", http.StatusInternalServerError, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_DECRYPTION_FAILED", "falha ao inicializar GCM", http.StatusInternalServerError, err)
	}
	sealed := append(ciphertext, tag...)
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, security.Wrap("ERR_TOKEN_DECRYPTION_FAILED", "falha ao descriptografar credencial", http.StatusInternalServerError, err)
	}
	var credential StoredOAuthCredential
	if err := json.Unmarshal(plain, &credential); err != nil {
		return nil, security.Wrap("ERR_TOKEN_DECRYPTION_FAILED", "credencial descriptografada invalida", http.StatusInternalServerError, err)
	}
	return &credential, nil
}
