package grok

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

type utlsTransport struct {
	proxyFunc  func(*http.Request) (*url.URL, error)
	httpsTrans *http.Transport
	httpTrans  *http.Transport
}

func newUTLSTransport(proxyFunc func(*http.Request) (*url.URL, error)) http.RoundTripper {
	t := &utlsTransport{
		proxyFunc: proxyFunc,
		httpTrans: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			MaxConnsPerHost:       200,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			Proxy:                 proxyFunc,
		},
	}
	t.httpsTrans = &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       200,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		DialTLSContext:        t.dialTLSContext,
	}
	_ = http2.ConfigureTransport(t.httpsTrans)
	return t
}

func (t *utlsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return t.httpTrans.RoundTrip(req)
	}
	return t.httpsTrans.RoundTrip(req)
}

func (t *utlsTransport) proxyURLForAddr(addr string) (*url.URL, error) {
	if t == nil || t.proxyFunc == nil {
		return nil, nil
	}
	req := &http.Request{
		URL: &url.URL{
			Scheme: "https",
			Host:   addr,
		},
	}
	return t.proxyFunc(req)
}

func (t *utlsTransport) dialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second}

	proxyURL, err := t.proxyURLForAddr(addr)
	if err != nil {
		return nil, err
	}

	var rawConn net.Conn
	if proxyURL != nil {
		rawConn, err = dialProxyConnect(ctx, dialer, proxyURL, addr)
	} else {
		rawConn, err = dialer.DialContext(ctx, network, addr)
	}
	if err != nil {
		return nil, err
	}

	host, _, _ := net.SplitHostPort(addr)
	config := &utls.Config{
		ServerName: host,
		NextProtos: []string{"h2", "http/1.1"},
	}

	spec, specErr := utls.UTLSIdToSpec(utls.HelloChrome_131)
	uconn := utls.UClient(rawConn, config, utls.HelloCustom)
	if specErr == nil {
		if err := uconn.ApplyPreset(&spec); err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("utls apply preset: %w", err)
		}
	}
	if err := uconn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("utls handshake: %w", err)
	}
	return uconn, nil
}

func dialProxyConnect(ctx context.Context, dialer *net.Dialer, proxyURL *url.URL, addr string) (net.Conn, error) {
	conn, err := dialer.DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("proxy dial failed: %w", err)
	}

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)
	if proxyURL.User != nil {
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()
		auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", auth)
	}
	connectReq += "\r\n"
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy connect write failed: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("proxy connect failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy connect status: %d", resp.StatusCode)
	}

	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, br: br}, nil
	}
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	br *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	return c.br.Read(b)
}
