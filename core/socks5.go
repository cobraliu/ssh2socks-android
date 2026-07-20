package core

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
)

// SOCKS5 server (RFC 1928), no-auth, CONNECT only. Each accepted connection is
// dialed out through the SSH chain. UDP ASSOCIATE is intentionally unsupported.
type socksServer struct {
	ln     net.Listener
	dialer *Dialer
	logf   func(string)
	wg     sync.WaitGroup
}

func startSocks(listenAddr string, dialer *Dialer, logf func(string)) (*socksServer, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}
	s := &socksServer{ln: ln, dialer: dialer, logf: logf}
	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

func (s *socksServer) addr() string { return s.ln.Addr().String() }

func (s *socksServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go s.handle(conn)
	}
}

func (s *socksServer) close() {
	_ = s.ln.Close()
	s.wg.Wait()
}

func (s *socksServer) handle(client net.Conn) {
	defer client.Close()
	target, err := s.handshake(client)
	if err != nil {
		if s.logf != nil {
			s.logf("SOCKS: " + err.Error())
		}
		return
	}
	remote, err := s.dialer.DialTCP(target)
	if err != nil {
		writeReply(client, 0x05) // connection refused
		if s.logf != nil {
			s.logf(fmt.Sprintf("拨号 %s 失败: %v", target, err))
		}
		return
	}
	defer remote.Close()
	writeReply(client, 0x00) // succeeded

	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, client); done <- struct{}{} }()
	go func() { io.Copy(client, remote); done <- struct{}{} }()
	<-done
}

// handshake performs the greeting + CONNECT request, returning "host:port".
func (s *socksServer) handshake(c net.Conn) (string, error) {
	buf := make([]byte, 262)
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return "", err
	}
	if buf[0] != 0x05 {
		return "", fmt.Errorf("非 SOCKS5 请求")
	}
	nmethods := int(buf[1])
	if _, err := io.ReadFull(c, buf[:nmethods]); err != nil {
		return "", err
	}
	// reply: no authentication required
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return "", err
	}
	// request: VER CMD RSV ATYP ...
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return "", err
	}
	if buf[1] != 0x01 { // CONNECT only
		writeReply(c, 0x07) // command not supported
		return "", fmt.Errorf("仅支持 CONNECT")
	}
	var host string
	switch buf[3] {
	case 0x01: // IPv4
		if _, err := io.ReadFull(c, buf[:4]); err != nil {
			return "", err
		}
		host = net.IP(buf[:4]).String()
	case 0x04: // IPv6
		if _, err := io.ReadFull(c, buf[:16]); err != nil {
			return "", err
		}
		host = net.IP(buf[:16]).String()
	case 0x03: // domain
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return "", err
		}
		n := int(buf[0])
		if _, err := io.ReadFull(c, buf[:n]); err != nil {
			return "", err
		}
		host = string(buf[:n])
	default:
		writeReply(c, 0x08) // address type not supported
		return "", fmt.Errorf("不支持的地址类型")
	}
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(buf[:2])
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

// writeReply sends a SOCKS5 reply with the given status and a zero bind addr.
func writeReply(c net.Conn, status byte) {
	_, _ = c.Write([]byte{0x05, status, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}
