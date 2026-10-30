package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"household/internal/value"

	"golang.org/x/crypto/hkdf"
)

type clientFunc func(*http.Request) (*http.Response, error)

func (f clientFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func receiverFixture(t *testing.T) (Receiver, *ecdh.PrivateKey) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Receiver{Endpoint: "https://fcm.googleapis.com/opaque%2fsegment?token=A%2B%2F&empty=", Keys: ReceiverKeys{P256DH: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}}, key
}

func deriveFixture(t *testing.T, secret, salt, info []byte, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, salt, info), value); err != nil {
		t.Fatal("derive controlled receiver material failed")
	}
	return value
}

func decryptFixture(t *testing.T, raw []byte, key *ecdh.PrivateKey, authKey string) []byte {
	t.Helper()
	if len(raw) < 86 || raw[20] != 65 {
		t.Fatal("invalid encrypted wire header")
	}
	server, err := ecdh.P256().NewPublicKey(raw[21:86])
	if err != nil {
		t.Fatal("invalid ephemeral sender")
	}
	shared, err := key.ECDH(server)
	if err != nil {
		t.Fatal("receiver agreement failed")
	}
	authSecret, err := decodeKey(authKey, 16)
	if err != nil {
		t.Fatal(err)
	}
	info := append([]byte("WebPush: info\x00"), key.PublicKey().Bytes()...)
	info = append(info, server.Bytes()...)
	ikm := deriveFixture(t, shared, authSecret, info, 32)
	contentKey := deriveFixture(t, ikm, raw[:16], []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := deriveFixture(t, ikm, raw[:16], []byte("Content-Encoding: nonce\x00"), 12)
	block, err := aes.NewCipher(contentKey)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	clear, err := gcm.Open(nil, nonce, raw[86:], nil)
	if err != nil {
		t.Fatal("actual sender ciphertext could not be decrypted")
	}
	clear = bytes.TrimRight(clear, "\x00")
	if len(clear) == 0 || clear[len(clear)-1] != 2 {
		t.Fatal("encrypted record delimiter missing")
	}
	return clear[:len(clear)-1]
}

func TestPushActualEncryptedWireAndVAPIDSubject(t *testing.T) {
	private, public, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	receiver, receiverPrivate := receiverFixture(t)
	s := newSender(public, private, "mailto:operator@example.org")
	defer s.close()
	notice := pushPayload{Schema: 1, Kind: "shopping_notice", ID: value.UUID(), ExpiresAt: time.Now().Add(time.Minute).Unix(), AudienceScope: strings.Repeat("a", 64)}
	payload, _ := json.Marshal(notice)
	calls := 0
	s.client = clientFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != "POST" || request.URL.String() != receiver.Endpoint || request.Header.Get("Content-Encoding") != "aes128gcm" || len(request.Header.Get("Topic")) != 32 {
			t.Fatal("actual request altered opaque endpoint or protocol headers")
		}
		deadline, ok := request.Context().Deadline()
		if !ok || deadline.After(time.Now().Add(4*time.Second)) {
			t.Fatal("request context lost its finite deadline")
		}
		wire, err := io.ReadAll(io.LimitReader(request.Body, 8193))
		if err != nil || len(wire) > 8192 || bytes.Contains(wire, payload) {
			t.Fatal("fixed private payload was not encrypted within bounds")
		}
		clear := decryptFixture(t, wire, receiverPrivate, receiver.Keys.Auth)
		if !bytes.Equal(clear, payload) {
			t.Fatal("actual receiver plaintext differs from the closed payload")
		}
		authorization := request.Header.Get("Authorization")
		pieces := strings.Split(strings.TrimPrefix(strings.Split(authorization, ",")[0], "vapid t="), ".")
		if len(pieces) != 3 {
			t.Fatal("actual VAPID token missing")
		}
		claimsRaw, err := base64.RawURLEncoding.DecodeString(pieces[1])
		if err != nil {
			t.Fatal("VAPID claims cannot be decoded")
		}
		var claims map[string]any
		if json.Unmarshal(claimsRaw, &claims) != nil || claims["sub"] != "mailto:operator@example.org" || claims["aud"] != "https://fcm.googleapis.com" {
			t.Fatal("VAPID subject or audience changed")
		}
		signature, err := base64.RawURLEncoding.DecodeString(pieces[2])
		if err != nil || len(signature) != 64 {
			t.Fatal("VAPID signature missing")
		}
		pubBytes, err := decodeKey(public, 65)
		if err != nil {
			t.Fatal(err)
		}
		publicKey, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pubBytes)
		if err != nil {
			t.Fatal("VAPID public key cannot be parsed")
		}
		sum := sha256.Sum256([]byte(pieces[0] + "." + pieces[1]))
		if !ecdsa.Verify(publicKey, sum[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
			t.Fatal("actual VAPID signature does not match configured public key")
		}
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	result := s.send(t.Context(), receiver, payload, strings.Repeat("a", 32))
	if calls != 1 || result.code != "gateway_accepted" || result.status != 201 {
		t.Fatal("actual encrypted request was not recorded as gateway acceptance")
	}
}

func TestPushGatewayStatesAndCanceledConcurrencyStayExplicit(t *testing.T) {
	private, public, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	receiver, _ := receiverFixture(t)
	payload, _ := json.Marshal(pushPayload{Schema: 1, Kind: "shopping_notice", ID: value.UUID(), ExpiresAt: time.Now().Add(time.Hour).Unix(), AudienceScope: strings.Repeat("a", 64)})
	for _, tc := range []struct {
		status int
		code   string
	}{{201, "gateway_accepted"}, {404, "connection_expired"}, {410, "connection_expired"}, {429, "gateway_limited"}, {503, "gateway_temporary"}, {401, "vapid_rejected"}, {302, "gateway_rejected"}} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			s := newSender(public, private, "https://example.org/contact")
			defer s.close()
			calls := 0
			s.client = clientFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"3600"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 17<<10)))}, nil
			})
			result := s.send(t.Context(), receiver, payload, strings.Repeat("b", 32))
			if calls != 1 || result.code != tc.code {
				t.Fatal("gateway outcome was misclassified")
			}
			if tc.status == 429 && !result.retryAt.After(time.Now().Add(59*time.Minute)) {
				t.Fatal("Retry-After lost behind an oversized body")
			}
		})
	}
	s := newSender(public, private, "mailto:operator@example.org")
	defer s.close()
	calls := 0
	s.client = clientFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, context.Canceled })
	s.slots <- struct{}{}
	s.slots <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := s.send(ctx, receiver, payload, strings.Repeat("b", 32))
	if calls != 0 || result.code != "network_timeout" {
		t.Fatal("queued cancellation reached HTTP")
	}
}
