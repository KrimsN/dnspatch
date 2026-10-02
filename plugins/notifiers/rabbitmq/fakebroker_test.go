package rabbitmq

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	frameMethod = 1
	frameBody   = 3
	frameEnd    = 0xCE
)

// method identifies an AMQP 0-9-1 method by its class and method numbers.
type method struct{ class, id uint16 }

var (
	connectionStart  = method{10, 10}
	connectionTuneOK = method{10, 31}
	connectionOpen   = method{10, 40}
	connectionClose  = method{10, 50}
	channelOpen      = method{20, 10}
	channelClose     = method{20, 40}
	exchangeDeclare  = method{40, 10}
	confirmSelect    = method{85, 10}
	basicPublish     = method{60, 40}
	basicAck         = method{60, 80}
	basicNack        = method{60, 120}
)

// published is what the fake broker saw in one basic.publish.
type published struct {
	exchange   string
	routingKey string
	body       []byte
}

// declared is what the fake broker saw in one exchange.declare.
type declared struct {
	name    string
	kind    string
	durable bool
}

// fakeBroker is a minimal AMQP 0-9-1 broker for the tests: it speaks just
// enough of the protocol (handshake, one channel, exchange.declare,
// confirm.select, basic.publish with confirms) for the notifier to run
// against it, so no RabbitMQ is needed.
type fakeBroker struct {
	ln net.Listener

	// nack makes the broker reject every message instead of confirming it.
	nack atomic.Bool
	// silent makes the broker accept messages without confirming them.
	silent atomic.Bool
	// declareFails makes exchange.declare close the channel with an error.
	declareFails atomic.Bool

	// breakAt lists the methods at which the broker drops the TCP connection.
	breakAt map[method]bool

	received chan published
	declares chan declared

	mu       sync.Mutex
	conns    []net.Conn
	connects int
}

func newFakeBroker(t *testing.T, breakAt ...method) *fakeBroker {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	b := &fakeBroker{
		ln:       ln,
		breakAt:  map[method]bool{},
		received: make(chan published, 16),
		declares: make(chan declared, 16),
	}

	for _, m := range breakAt {
		b.breakAt[m] = true
	}

	t.Cleanup(func() {
		_ = ln.Close()
		b.dropConnections()
	})

	go b.accept()

	return b
}

func (b *fakeBroker) url() string { return "amqp://guest:guest@" + b.ln.Addr().String() + "/" }

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
		b.connects++
		b.mu.Unlock()

		go b.serve(conn)
	}
}

func (b *fakeBroker) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// The protocol header the client opens with: "AMQP" 0 0 9 1.
	if _, err := io.ReadFull(conn, make([]byte, 8)); err != nil {
		return
	}

	if writeMethod(conn, 0, connectionStart, []byte{0, 9}, longStr(""), longStr("PLAIN"), longStr("en_US")) != nil {
		return
	}

	var tag uint64

	for {
		typ, channel, payload, err := readFrame(conn)
		if err != nil {
			return
		}

		if typ != frameMethod {
			continue
		}

		m := method{binary.BigEndian.Uint16(payload), binary.BigEndian.Uint16(payload[2:])}
		args := payload[4:]

		if b.breakAt[m] {
			return
		}

		switch m {
		case method{10, 11}:
			tune := binary.BigEndian.AppendUint16(nil, 0xFFFF)
			tune = binary.BigEndian.AppendUint32(tune, 131072)
			tune = binary.BigEndian.AppendUint16(tune, 0)
			err = writeMethod(conn, 0, method{10, 30}, tune)
		case connectionTuneOK, method{20, 41}:
			// nothing to answer
		case connectionOpen:
			err = writeMethod(conn, 0, method{10, 41}, shortStr(""))
		case connectionClose:
			_ = writeMethod(conn, 0, method{10, 51})

			return
		case channelOpen:
			err = writeMethod(conn, channel, method{20, 11}, longStr(""))
		case channelClose:
			err = writeMethod(conn, channel, method{20, 41})
		case exchangeDeclare:
			err = b.declare(conn, channel, args)
		case confirmSelect:
			err = writeMethod(conn, channel, method{85, 11})
		case basicPublish:
			tag++
			err = b.publish(conn, channel, args, tag)
		}

		if err != nil {
			return
		}
	}
}

func (b *fakeBroker) declare(conn net.Conn, channel uint16, args []byte) error {
	name, rest := readShort(args[2:])
	kind, rest := readShort(rest)

	b.declares <- declared{name: name, kind: kind, durable: rest[0]&2 != 0}

	if b.declareFails.Load() {
		// 406 precondition failed, the way a real broker refuses a clash.
		return writeMethod(conn, channel, channelClose, binary.BigEndian.AppendUint16(nil, 406), shortStr("PRECONDITION_FAILED"), []byte{0, 40, 0, 10})
	}

	return writeMethod(conn, channel, method{40, 11})
}

// publish reads the content that follows a basic.publish and answers it with
// the confirmation the test asked for.
func (b *fakeBroker) publish(conn net.Conn, channel uint16, args []byte, tag uint64) error {
	exchange, rest := readShort(args[2:])
	routingKey, _ := readShort(rest)

	_, _, header, err := readFrame(conn)
	if err != nil {
		return err
	}

	size := binary.BigEndian.Uint64(header[4:])
	body := make([]byte, 0, size)

	for uint64(len(body)) < size {
		typ, _, chunk, err := readFrame(conn)
		if err != nil {
			return err
		}

		if typ == frameBody {
			body = append(body, chunk...)
		}
	}

	b.received <- published{exchange: exchange, routingKey: routingKey, body: body}

	switch {
	case b.silent.Load():
		return nil
	case b.nack.Load():
		return writeMethod(conn, channel, basicNack, binary.BigEndian.AppendUint64(nil, tag), []byte{0})
	default:
		return writeMethod(conn, channel, basicAck, binary.BigEndian.AppendUint64(nil, tag), []byte{0})
	}
}

func readFrame(r io.Reader) (typ byte, channel uint16, payload []byte, err error) {
	var head [7]byte

	if _, err = io.ReadFull(r, head[:]); err != nil {
		return 0, 0, nil, err
	}

	payload = make([]byte, binary.BigEndian.Uint32(head[3:]))

	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, 0, nil, err
	}

	var end [1]byte

	if _, err = io.ReadFull(r, end[:]); err != nil {
		return 0, 0, nil, err
	}

	return head[0], binary.BigEndian.Uint16(head[1:]), payload, nil
}

func writeMethod(w io.Writer, channel uint16, m method, args ...[]byte) error {
	payload := binary.BigEndian.AppendUint16(nil, m.class)
	payload = binary.BigEndian.AppendUint16(payload, m.id)

	for _, a := range args {
		payload = append(payload, a...)
	}

	frame := []byte{frameMethod}
	frame = binary.BigEndian.AppendUint16(frame, channel)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)
	frame = append(frame, frameEnd)

	_, err := w.Write(frame)

	return err
}

func shortStr(s string) []byte { return append([]byte{byte(len(s))}, s...) }

// longStr is a string with a 32-bit length; an empty one doubles as an empty
// field table, which has the same encoding.
func longStr(s string) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(s))), s...)
}

func readShort(b []byte) (string, []byte) {
	n := int(b[0])

	return string(b[1 : 1+n]), b[1+n:]
}
