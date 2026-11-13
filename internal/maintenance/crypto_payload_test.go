package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Only the methods consumed by readProtected are implemented; unexpected row
// operations fail through the nil embedded interface.
type cryptoPayloadRows struct {
	pgx.Rows
	payload []byte
	yielded bool
}

func (r *cryptoPayloadRows) Next() bool {
	if r.yielded {
		return false
	}
	r.yielded = true
	return true
}

func (r *cryptoPayloadRows) Scan(dest ...any) error {
	*dest[0].(*string) = "00000000-0000-4000-8000-000000000001"
	*dest[1].(*string) = ""
	*dest[2].(*string) = ""
	*dest[3].(*string) = "passkey-login"
	*dest[4].(*int16) = 1
	*dest[5].(*string) = "v1"
	*dest[6].(*[]byte) = bytes.Clone(r.payload)
	return nil
}

func (*cryptoPayloadRows) Close()     {}
func (*cryptoPayloadRows) Err() error { return nil }

type cryptoPayloadReader struct{ rows *cryptoPayloadRows }

func (r cryptoPayloadReader) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return r.rows, nil
}

func TestCryptoMaintenanceChallengePreservesOriginalJSON(t *testing.T) {
	sealed := bytes.Repeat([]byte{0xc9, 0xf6, 0, 0xff}, 12)
	original, err := json.Marshal(map[string][]byte{"sealed": sealed})
	if err != nil {
		t.Fatal(err)
	}
	reader := cryptoPayloadReader{rows: &cryptoPayloadRows{payload: original}}
	table := protectedTable{"auth_challenges", "id", "", "payload", "purpose='passkey-login'", 131072}
	rows, err := readProtected(t.Context(), reader, table, "", 1, false, "")
	if err != nil || len(rows) != 1 {
		t.Fatal("read challenge", err, len(rows))
	}
	row := rows[0]
	if !bytes.Equal(row.original, original) || !json.Valid(row.original) {
		t.Fatal("challenge decoding corrupted the original JSON compare-and-swap value")
	}
	if !bytes.Equal(row.stored.Ciphertext, sealed) {
		t.Fatal("decoded challenge ciphertext differs")
	}
	row.stored.Ciphertext[0] ^= 0xff
	if !bytes.Equal(row.original, original) {
		t.Fatal("decoded ciphertext aliases the original JSON compare-and-swap value")
	}
}
