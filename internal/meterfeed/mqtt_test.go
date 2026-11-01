package meterfeed

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestMQTTFramesEnforceFiniteCanonicalLengths(t *testing.T) {
	for _, length := range []int{0, 1, 127, 128, 16384, 64 << 10} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			var wire bytes.Buffer
			payload := bytes.Repeat([]byte{'x'}, length)
			if err := writePacket(&wire, 0x30, payload); err != nil {
				t.Fatal(err)
			}
			header, body, err := readPacket(&wire)
			if err != nil || header != 0x30 || !bytes.Equal(body, payload) {
				t.Fatal(header, len(body), err)
			}
		})
	}
	for _, raw := range [][]byte{{0x30, 0x80, 0}, {0x30, 0xff, 0xff, 0xff, 0x7f}, {0x30, 0x80, 0x80, 0x80, 0x80}, {0x30, 2, 1}} {
		if _, _, err := readPacket(bytes.NewReader(raw)); err == nil {
			t.Fatal("unbounded, noncanonical or truncated packet accepted")
		}
	}
	if err := writePacket(io.Discard, 0x30, make([]byte, (64<<10)+1)); err == nil {
		t.Fatal("outbound packet bound bypassed")
	}
}

func TestMQTTRealCounterpartBindsPubackToCommittedCallback(t *testing.T) {
	for _, tc := range []struct {
		name                string
		invalid, failCommit bool
	}{
		{"committed sample", false, false}, {"durably rejected poison", true, false}, {"uncommitted sample", false, true}, {"uncommitted rejection", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			var committed atomic.Bool
			broker := make(chan error, 1)
			go func() {
				broker <- func() error {
					conn, err := listener.Accept()
					if err != nil {
						return err
					}
					defer func() { _ = conn.Close() }()
					if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
						return err
					}
					header, connect, err := readPacket(conn)
					if err != nil || header != 0x10 || len(connect) < 10 || connect[7] != 0xc0 {
						return fmt.Errorf("authenticated persistent connect missing: %w", err)
					}
					if err = writePacket(conn, 0x20, []byte{0, 0}); err != nil {
						return err
					}
					header, subscribe, err := readPacket(conn)
					if err != nil || header != 0x82 || len(subscribe) < 5 || subscribe[len(subscribe)-1] != 1 {
						return fmt.Errorf("exact QoS1 subscription missing: %w", err)
					}
					topicLength := int(binary.BigEndian.Uint16(subscribe[2:4]))
					if topicLength != len(testSource().Name) || 4+topicLength >= len(subscribe) || string(subscribe[4:4+topicLength]) != testSource().Name {
						return errors.New("unexpected topic")
					}
					// Retained messages may arrive before SUBACK. The callback must still commit first.
					topic, _ := mqttString(testSource().Name)
					payload := feedMessage
					if tc.invalid {
						payload = `{"value":null}`
					}
					publish := append(append(topic, 0, 7), []byte(payload)...)
					if err = writePacket(conn, 0x33, publish); err != nil {
						return err
					}
					header, ack, err := readPacket(conn)
					if tc.failCommit {
						if err == nil {
							return errors.New("acknowledged an uncommitted callback")
						}
						return nil
					}
					if err != nil || header != 0x40 || !bytes.Equal(ack, []byte{0, 7}) || !committed.Load() {
						return errors.New("PUBACK preceded committed sample/rejection")
					}
					if err = writePacket(conn, 0x90, []byte{0, 1, 1}); err != nil {
						return err
					}
					return nil
				}()
			}()
			failure := errors.New("synthetic durable commit failure")
			stats, consumeErr := ConsumeMQTT(t.Context(), Config{MQTTURL: "mqtt://" + listener.Addr().String(), MQTTUser: "test-user", MQTTPassword: "synthetic-test-password", AllowPlaintext: true}, testSource(),
				func(context.Context, Sample) (bool, error) {
					if tc.failCommit {
						return false, failure
					}
					committed.Store(true)
					return true, nil
				},
				func(context.Context, string) error {
					if tc.failCommit {
						return failure
					}
					committed.Store(true)
					return nil
				})
			if err = <-broker; err != nil {
				t.Fatal(err)
			}
			if tc.failCommit {
				if !errors.Is(consumeErr, failure) || committed.Load() {
					t.Fatal(stats, consumeErr)
				}
				return
			}
			if !errors.Is(consumeErr, ErrUnavailable) || !stats.Subscribed || stats.QoS != 1 || tc.invalid && stats.Rejected != 1 || !tc.invalid && stats.Inserted != 1 {
				t.Fatal(stats, consumeErr)
			}
		})
	}
}

func FuzzMQTTFrameReaderNeverExceedsBounds(f *testing.F) {
	f.Add([]byte{0x30, 0})
	f.Add([]byte{0x30, 0xff, 0xff, 0xff, 0x7f})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, body, err := readPacket(bytes.NewReader(raw))
		if err == nil && len(body) > 64<<10 {
			t.Fatal("frame budget exceeded")
		}
	})
}
