package mqtt

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/eclipse/paho.mqtt.golang/packets"
)

// published is what the fake broker saw in one PUBLISH packet.
type published struct {
	topic   string
	payload []byte
	qos     byte
	retain  bool
}

// fakeBroker is a minimal MQTT 3.1.1 broker for the tests: it accepts
// connections, acknowledges PUBLISH at every QoS and records what it got, so
// the notifier is exercised without an external broker.
type fakeBroker struct {
	ln net.Listener

	// connackCode is the return code sent in answer to CONNECT; 0 accepts.
	connackCode byte
	// silent makes the broker read PUBLISH without acknowledging it.
	silent atomic.Bool

	received chan published

	mu       sync.Mutex
	conns    []net.Conn
	connects int
}

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	b := &fakeBroker{ln: ln, received: make(chan published, 16)}

	t.Cleanup(func() {
		_ = ln.Close()
		b.dropConnections()
	})

	go b.accept()

	return b
}

func (b *fakeBroker) url() string { return "mqtt://" + b.ln.Addr().String() }

func (b *fakeBroker) connectCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.connects
}

// dropConnections closes every client connection from the broker's side.
func (b *fakeBroker) dropConnections() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, c := range b.conns {
		_ = c.Close()
	}

	b.conns = nil
}

func (b *fakeBroker) accept() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			return
		}

		b.mu.Lock()
		b.conns = append(b.conns, conn)
		b.mu.Unlock()

		go b.serve(conn)
	}
}

func (b *fakeBroker) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	for {
		cp, err := packets.ReadPacket(conn)
		if err != nil {
			return
		}

		switch p := cp.(type) {
		case *packets.ConnectPacket:
			b.mu.Lock()
			b.connects++
			b.mu.Unlock()

			ack := packets.NewControlPacket(packets.Connack).(*packets.ConnackPacket)
			ack.ReturnCode = b.connackCode

			if ack.Write(conn) != nil || b.connackCode != 0 {
				return
			}
		case *packets.PublishPacket:
			b.received <- published{topic: p.TopicName, payload: p.Payload, qos: p.Qos, retain: p.Retain}

			if b.silent.Load() {
				continue
			}

			if !b.acknowledge(conn, p) {
				return
			}
		case *packets.PubrelPacket:
			comp := packets.NewControlPacket(packets.Pubcomp).(*packets.PubcompPacket)
			comp.MessageID = p.MessageID

			if comp.Write(conn) != nil {
				return
			}
		case *packets.PingreqPacket:
			if packets.NewControlPacket(packets.Pingresp).Write(conn) != nil {
				return
			}
		case *packets.DisconnectPacket:
			return
		}
	}
}

// acknowledge answers a PUBLISH the way its QoS requires and reports whether
// the connection is still usable.
func (b *fakeBroker) acknowledge(conn net.Conn, p *packets.PublishPacket) bool {
	switch p.Qos {
	case 1:
		ack := packets.NewControlPacket(packets.Puback).(*packets.PubackPacket)
		ack.MessageID = p.MessageID

		return ack.Write(conn) == nil
	case 2:
		rec := packets.NewControlPacket(packets.Pubrec).(*packets.PubrecPacket)
		rec.MessageID = p.MessageID

		return rec.Write(conn) == nil
	default:
		return true
	}
}
