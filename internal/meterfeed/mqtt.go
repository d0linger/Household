package meterfeed

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/url"
	"strconv"
	"time"
)

type MQTTStats struct {
	Packets    int
	Inserted   int
	Duplicates int
	Rejected   int
	Subscribed bool
	QoS        byte
}

func mqttString(text string) ([]byte, error) {
	if len(text) > 8192 {
		return nil, ErrConfiguration
	}
	encoded := make([]byte, len(text)+2)
	encoded[0], encoded[1] = byte((len(text)>>8)&255), byte(len(text)&255)
	copy(encoded[2:], text)
	return encoded, nil
}

func writePacket(writer io.Writer, header byte, body []byte) error {
	if len(body) > 64<<10 {
		return ErrMessage
	}
	prefix := []byte{header}
	remaining := len(body)
	for {
		digit := byte(remaining % 128)
		remaining /= 128
		if remaining != 0 {
			digit |= 128
		}
		prefix = append(prefix, digit)
		if remaining == 0 {
			break
		}
	}
	packet := append(prefix, body...)
	for len(packet) != 0 {
		written, err := writer.Write(packet)
		if err != nil || written == 0 {
			return ErrUnavailable
		}
		packet = packet[written:]
	}
	return nil
}

func readPacket(reader io.Reader) (byte, []byte, error) {
	var header [1]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, nil, err
	}
	length, multiplier := 0, 1
	for index := 0; index < 4; index++ {
		var encoded [1]byte
		if _, err := io.ReadFull(reader, encoded[:]); err != nil {
			return 0, nil, err
		}
		length += int(encoded[0]&127) * multiplier
		if length > 64<<10 {
			return 0, nil, ErrMessage
		}
		if encoded[0]&128 == 0 {
			if index > 0 && encoded[0] == 0 {
				return 0, nil, ErrMessage
			}
			body := make([]byte, length)
			_, err := io.ReadFull(reader, body)
			return header[0], body, err
		}
		multiplier *= 128
	}
	return 0, nil, ErrMessage
}

// ConsumeMQTT uses a persistent MQTT3.1.1 session and requests QoS1. A publish
// acknowledgement follows only a committed sample or committed rejection.
func ConsumeMQTT(ctx context.Context, config Config, source Source,
	handle func(context.Context, Sample) (bool, error), reject func(context.Context, string) error,
) (MQTTStats, error) {
	stats := MQTTStats{}
	if Validate(config) != nil || config.MQTTURL == "" || !ValidTopic(source.Name) || handle == nil || reject == nil {
		return stats, ErrConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u, err := url.Parse(config.MQTTURL)
	if err != nil {
		return stats, ErrConfiguration
	}
	port := u.Port()
	if port == "" {
		port = "8883"
		if u.Scheme == "mqtt" {
			port = "1883"
		}
	}
	address := net.JoinHostPort(u.Hostname(), port)
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	var connection net.Conn
	if u.Scheme == "mqtts" {
		var tlsConfig *tls.Config
		tlsConfig, err = transportTLS(config)
		if err != nil {
			return stats, err
		}
		connection, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return stats, ErrUnavailable
	}
	defer func() { _ = connection.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err = connection.SetDeadline(deadline); err != nil {
		return stats, ErrUnavailable
	}
	digest := sha256.Sum256([]byte(source.ID + "/" + strconv.FormatInt(source.Generation, 10)))
	clientID, _ := mqttString("hh" + hex.EncodeToString(digest[:])[:20])
	username, _ := mqttString(config.MQTTUser)
	password, _ := mqttString(config.MQTTPassword)
	// Protocol name, level4, username/password, persistent session, keepalive30s.
	connect := append([]byte{0, 4, 'M', 'Q', 'T', 'T', 4, 0xC0, 0, 30}, clientID...)
	connect = append(connect, username...)
	connect = append(connect, password...)
	if err = writePacket(connection, 0x10, connect); err != nil {
		return stats, err
	}
	header, body, err := readPacket(connection)
	if err != nil || header != 0x20 || len(body) != 2 || body[0]&0xFE != 0 || body[1] != 0 {
		return stats, ErrUnavailable
	}
	topic, err := mqttString(source.Name)
	if err != nil {
		return stats, err
	}
	subscribe := append([]byte{0, 1}, topic...)
	subscribe = append(subscribe, 1)
	if err = writePacket(connection, 0x82, subscribe); err != nil {
		return stats, err
	}
	for stats.Packets < 100 {
		header, body, err = readPacket(connection)
		if err != nil {
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) && stats.Subscribed {
					return stats, nil
				}
				return stats, ctx.Err()
			}
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() && stats.Subscribed {
				return stats, nil
			}
			return stats, ErrUnavailable
		}
		stats.Packets++
		switch header >> 4 {
		case 9:
			if header != 0x90 || len(body) != 3 || binary.BigEndian.Uint16(body[:2]) != 1 || body[2] > 1 || stats.Subscribed {
				return stats, ErrMessage
			}
			stats.Subscribed, stats.QoS = true, body[2]
		case 3:
			qos := (header >> 1) & 3
			if qos > 1 || qos == 0 && header&8 != 0 || len(body) < 2 {
				return stats, ErrMessage
			}
			length := int(binary.BigEndian.Uint16(body[:2]))
			if length == 0 || length+2 > len(body) || string(body[2:2+length]) != source.Name {
				return stats, ErrMessage
			}
			payload := body[2+length:]
			var packetID uint16
			if qos == 1 {
				if len(payload) < 2 {
					return stats, ErrMessage
				}
				packetID, payload = binary.BigEndian.Uint16(payload[:2]), payload[2:]
				if packetID == 0 {
					return stats, ErrMessage
				}
			}
			sample, parseErr := ParseMQTT(payload, source)
			if parseErr != nil {
				digest := sha256.Sum256(payload)
				if err = reject(ctx, hex.EncodeToString(digest[:])); err != nil {
					return stats, err
				}
				stats.Rejected++
			} else {
				inserted, err := handle(ctx, sample)
				if err != nil {
					if !errors.Is(err, ErrMessage) {
						return stats, err
					}
					digest := sha256.Sum256(payload)
					if err = reject(ctx, hex.EncodeToString(digest[:])); err != nil {
						return stats, err
					}
					stats.Rejected++
				} else if inserted {
					stats.Inserted++
				} else {
					stats.Duplicates++
				}
			}
			if qos == 1 {
				ack := []byte{byte(packetID >> 8), byte(packetID)}
				if err = writePacket(connection, 0x40, ack); err != nil {
					return stats, err
				}
			}
		default:
			return stats, ErrMessage
		}
	}
	if !stats.Subscribed {
		return stats, ErrUnavailable
	}
	_ = writePacket(connection, 0xE0, nil)
	return stats, nil
}
