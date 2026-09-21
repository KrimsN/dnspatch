// Package socks5test is a minimal SOCKS5 server for tests. It supports the
// CONNECT command with no authentication or with a login and password, and
// records what clients asked it to reach.
package socks5test

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Server is a running SOCKS5 proxy.
type Server struct {
	// Addr is the host:port the proxy listens on.
	Addr string

	listener net.Listener
	user     string
	pass     string

	mu      sync.Mutex
	targets []string
	routes  map[string]string
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
}

// Start runs a proxy on a loopback port until the test ends. A non-empty user
// makes it demand that login and password.
func Start(t testing.TB, user, pass string) *Server {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		Addr:     l.Addr().String(),
		listener: l,
		user:     user,
		pass:     pass,
		routes:   map[string]string{},
		conns:    map[net.Conn]struct{}{},
	}

	s.wg.Add(1)
	go s.accept()

	t.Cleanup(s.close)

	return s
}

// Route makes the proxy connect to dest whenever a client asks for the
// host:port name. It stands in for name resolution done by the proxy.
func (s *Server) Route(name, dest string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.routes[name] = dest
}

// Targets lists the destinations clients asked for, in order.
func (s *Server) Targets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.targets...)
}

func (s *Server) close() {
	_ = s.listener.Close()

	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
}

func (s *Server) accept() {
	defer s.wg.Done()

	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}

		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				_ = c.Close()

				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
			}()

			s.serve(c)
		}()
	}
}

func (s *Server) serve(c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))

	if err := s.handshake(c); err != nil {
		return
	}

	target, err := readRequest(c)
	if err != nil {
		return
	}

	s.mu.Lock()
	s.targets = append(s.targets, target)
	dest := target
	if routed, ok := s.routes[target]; ok {
		dest = routed
	}
	s.mu.Unlock()

	upstream, err := net.DialTimeout("tcp", dest, 5*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = upstream.Close() }()

	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})

	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
		done <- struct{}{}
	}
	go pipe(upstream, c)
	go pipe(c, upstream)
	<-done
	<-done
}

// handshake negotiates the authentication method and, when a login is
// required, checks it.
func (s *Server) handshake(c net.Conn) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(c, head); err != nil || head[0] != 5 {
		return errors.New("bad greeting")
	}

	methods := make([]byte, head[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return err
	}

	want := byte(0x00)
	if s.user != "" {
		want = 0x02
	}

	offered := false
	for _, m := range methods {
		offered = offered || m == want
	}
	if !offered {
		_, _ = c.Write([]byte{5, 0xFF})
		return errors.New("no acceptable method")
	}

	if _, err := c.Write([]byte{5, want}); err != nil {
		return err
	}
	if want == 0x00 {
		return nil
	}

	// RFC 1929: version, login, password.
	version := make([]byte, 1)
	if _, err := io.ReadFull(c, version); err != nil {
		return err
	}
	user, err := readString(c)
	if err != nil {
		return err
	}
	pass, err := readString(c)
	if err != nil {
		return err
	}

	if user != s.user || pass != s.pass {
		_, _ = c.Write([]byte{1, 1})
		return errors.New("bad credentials")
	}

	_, err = c.Write([]byte{1, 0})

	return err
}

// readRequest reads a CONNECT request and returns its host:port.
func readRequest(c net.Conn) (string, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		return "", err
	}
	if head[0] != 5 || head[1] != 1 {
		_, _ = c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return "", errors.New("only CONNECT is supported")
	}

	var host string
	switch head[3] {
	case 1:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(c, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	case 3:
		name, err := readString(c)
		if err != nil {
			return "", err
		}
		host = name
	case 4:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(c, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	default:
		return "", errors.New("unknown address type")
	}

	port := make([]byte, 2)
	if _, err := io.ReadFull(c, port); err != nil {
		return "", err
	}

	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))), nil
}

// readString reads a string prefixed with a one-byte length.
func readString(r io.Reader) (string, error) {
	size := make([]byte, 1)
	if _, err := io.ReadFull(r, size); err != nil {
		return "", err
	}

	buf := make([]byte, size[0])
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}

	return string(buf), nil
}
