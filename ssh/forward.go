package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"

	cryptossh "golang.org/x/crypto/ssh"
)

type ForwardSpec struct {
	BindAddress string
	BindPort    int
	RemoteHost  string
	RemotePort  int
}

func ParseForwardSpec(spec string) (ForwardSpec, error) {
	parts := strings.Split(strings.TrimSpace(spec), ":")
	switch len(parts) {
	case 3:
		localPort, err := parsePort(parts[0], "local")
		if err != nil {
			return ForwardSpec{}, err
		}
		remotePort, err := parsePort(parts[2], "remote")
		if err != nil {
			return ForwardSpec{}, err
		}
		return ForwardSpec{
			BindAddress: "127.0.0.1",
			BindPort:    localPort,
			RemoteHost:  strings.TrimSpace(parts[1]),
			RemotePort:  remotePort,
		}, nil
	case 4:
		localPort, err := parsePort(parts[1], "local")
		if err != nil {
			return ForwardSpec{}, err
		}
		remotePort, err := parsePort(parts[3], "remote")
		if err != nil {
			return ForwardSpec{}, err
		}
		return ForwardSpec{
			BindAddress: strings.TrimSpace(parts[0]),
			BindPort:    localPort,
			RemoteHost:  strings.TrimSpace(parts[2]),
			RemotePort:  remotePort,
		}, nil
	default:
		return ForwardSpec{}, fmt.Errorf("expected [bind_address:]local_port:remote_host:remote_port")
	}
}

func (s ForwardSpec) LocalAddress() string {
	return net.JoinHostPort(s.BindAddress, strconv.Itoa(s.BindPort))
}

func (s ForwardSpec) RemoteAddress() string {
	return net.JoinHostPort(s.RemoteHost, strconv.Itoa(s.RemotePort))
}

type ForwardSession struct {
	client    *cryptossh.Client
	listeners []net.Listener
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func StartLocalForwards(ctx context.Context, target Target, specs []string) (*ForwardSession, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	client, err := Dial(ctx, target)
	if err != nil {
		return nil, err
	}

	parsed := make([]ForwardSpec, 0, len(specs))
	for _, raw := range specs {
		spec, err := ParseForwardSpec(raw)
		if err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("invalid forward %q: %w", raw, err)
		}
		parsed = append(parsed, spec)
	}

	session := &ForwardSession{client: client}

	for _, spec := range parsed {
		listener, err := net.Listen("tcp", spec.LocalAddress())
		if err != nil {
			session.Close()
			return nil, fmt.Errorf("failed to listen on %s: %w", spec.LocalAddress(), err)
		}
		session.listeners = append(session.listeners, listener)
		session.wg.Add(1)
		go session.acceptLoop(listener, spec)
	}

	return session, nil
}

func (s *ForwardSession) Close() error {
	s.closeOnce.Do(func() {
		for _, listener := range s.listeners {
			_ = listener.Close()
		}
		if s.client != nil {
			_ = s.client.Close()
		}
		s.wg.Wait()
	})
	return nil
}

func (s *ForwardSession) acceptLoop(listener net.Listener, spec ForwardSpec) {
	defer s.wg.Done()
	for {
		localConn, err := listener.Accept()
		if err != nil {
			return
		}

		s.wg.Add(1)
		go func(conn net.Conn) {
			defer s.wg.Done()
			s.proxyConn(conn, spec)
		}(localConn)
	}
}

func (s *ForwardSession) proxyConn(localConn net.Conn, spec ForwardSpec) {
	defer localConn.Close()

	remoteConn, err := s.client.Dial("tcp", spec.RemoteAddress())
	if err != nil {
		return
	}
	defer remoteConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(remoteConn, localConn)
		if tcp, ok := remoteConn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(localConn, remoteConn)
		if tcp, ok := localConn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()

	wg.Wait()
}

func parsePort(raw string, label string) (int, error) {
	value := strings.TrimSpace(raw)
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s port %q is not a number", label, value)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s port %d is out of range", label, port)
	}
	return port, nil
}
