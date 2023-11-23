package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
)

func proxyListener() {
	l, err := net.Listen("tcp", config.Server.Proxy.Address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy: Failed to start listener: %v\n", err)
		return
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			continue
		}

		go proxyHandle(conn)
	}
}

func proxyHandle(conn net.Conn) {
	defer conn.Close()

	// Establish TLS.
	var target string

	tlsConfig := &tls.Config{
		GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
			target = info.ServerName

			cert, err := tls.X509KeyPair([]byte(config.Server.Proxy.Certificate), []byte(config.Server.Proxy.Key))
			if err != nil {
				return nil, err
			}

			return &cert, nil
		},

		GetClientCertificate: func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			cert, err := tls.X509KeyPair([]byte(config.Incus.Client.Certificate), []byte(config.Incus.Client.Key))
			if err != nil {
				return nil, err
			}

			return &cert, nil
		},

		InsecureSkipVerify: true,
	}

	tlsConn := tls.Server(conn, tlsConfig)
	err := tlsConn.Handshake()
	if err != nil {
		return
	}

	id := strings.Split(target, ".")[0]

	// Get the instance.
	sessionId, _, instanceIP, _, _, _, err := dbGetInstance(id, true)
	if err != nil || sessionId == -1 || instanceIP == "" {
		return
	}

	// Connect to Incus.
	backendConn, err := tls.Dial("tcp", net.JoinHostPort(instanceIP, "8443"), tlsConfig)
	if err != nil {
		return
	}
	defer backendConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		io.Copy(tlsConn, backendConn)
		tlsConn.CloseWrite()
		wg.Done()
	}()

	go func() {
		io.Copy(backendConn, tlsConn)
		backendConn.CloseWrite()
		wg.Done()
	}()

	wg.Wait()
}
