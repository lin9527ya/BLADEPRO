package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lin9527ya/bladepro/agent"
)

// Chrome does not reliably support proxy userinfo. A private per-run loopback
// relay binds every navigation and subresource to the source task instead.
func (s *Server) srcBrowserProxy(ctx context.Context, taskID int64) (string, func(), error) {
	upstream := strings.TrimSpace(s.m.ProxyAddr())
	if upstream != "" && !strings.Contains(upstream, "://") {
		upstream = "http://" + upstream
	}
	var proxyURL *url.URL
	var err error
	if upstream != "" {
		proxyURL, err = url.Parse(agent.ProxyURLForTask(upstream, taskID))
		if err != nil {
			return "", nil, err
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	if proxyURL != nil {
		tr.Proxy = http.ProxyURL(proxyURL)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	var mu sync.Mutex
	tunnels := map[net.Conn]bool{}
	closing := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.URL.Hostname()
		if r.Method == http.MethodConnect {
			var parseErr error
			host, _, parseErr = net.SplitHostPort(r.Host)
			if parseErr != nil {
				http.Error(w, "invalid CONNECT target", http.StatusBadRequest)
				return
			}
		}
		if err := s.checkProxyTarget(taskID, host); err != nil {
			http.Error(w, "out of task scope", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodConnect {
			req := r.Clone(r.Context())
			req.RequestURI = ""
			req.Header.Del("Proxy-Authorization")
			resp, err := tr.RoundTrip(req)
			if err != nil {
				http.Error(w, "upstream unavailable", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			for k, values := range resp.Header {
				for _, v := range values {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
		address := r.Host
		if proxyURL != nil {
			address = proxyURL.Host
		}
		conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(r.Context(), "tcp", address)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		reader := bufio.NewReader(conn)
		if proxyURL != nil {
			connect := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: r.Host}, Host: r.Host, Header: make(http.Header)}
			connect.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("bp-task-"+strconv.FormatInt(taskID, 10)+":")))
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			if err := connect.Write(conn); err != nil {
				conn.Close()
				http.Error(w, "proxy unavailable", http.StatusBadGateway)
				return
			}
			resp, err := http.ReadResponse(reader, connect)
			if err != nil || resp.StatusCode != 200 {
				conn.Close()
				http.Error(w, "proxy refused tunnel", http.StatusBadGateway)
				return
			}
			_ = conn.SetDeadline(time.Time{})
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			conn.Close()
			http.Error(w, "tunnel unsupported", http.StatusInternalServerError)
			return
		}
		client, rw, err := hj.Hijack()
		if err != nil {
			conn.Close()
			return
		}
		mu.Lock()
		if closing {
			mu.Unlock()
			client.Close()
			conn.Close()
			return
		}
		tunnels[client] = true
		tunnels[conn] = true
		mu.Unlock()
		defer func() {
			client.Close()
			conn.Close()
			mu.Lock()
			delete(tunnels, client)
			delete(tunnels, conn)
			mu.Unlock()
		}()
		_, _ = fmt.Fprint(rw, "HTTP/1.1 200 Connection Established\r\n\r\n")
		if err := rw.Flush(); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { _, _ = io.Copy(conn, rw); conn.Close(); close(done) }()
		_, _ = io.Copy(client, reader)
		client.Close()
		conn.Close()
		<-done
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	goSafe("src/browser-proxy", nil, func() { _ = srv.Serve(ln) })
	closeProxy := func() {
		mu.Lock()
		closing = true
		mu.Unlock()
		_ = srv.Close()
		tr.CloseIdleConnections()
		mu.Lock()
		for conn := range tunnels {
			_ = conn.Close()
		}
		mu.Unlock()
	}
	return ln.Addr().String(), closeProxy, nil
}
