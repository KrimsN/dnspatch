package httpx

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/KrimsN/dnspatch/internal/socks5test"
)

const secret = "hunter2"

func TestParseProxy(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "socks5", raw: "socks5://203.0.113.5:1080"},
		{name: "socks5h", raw: "socks5h://proxy.example.com:1080"},
		{name: "http", raw: "http://proxy.example.com:3128"},
		{name: "https", raw: "https://proxy.example.com:443"},
		{name: "default port", raw: "socks5://proxy.example.com"},
		{name: "trailing slash", raw: "http://proxy.example.com:3128/"},
		{name: "ipv6", raw: "socks5://[2001:db8::5]:1080"},
		{name: "credentials", raw: "socks5://user:" + secret + "@proxy.example.com:1080"},
		{name: "encoded credentials", raw: "socks5://user:p%40ss%3A@proxy.example.com:1080"},

		{name: "ftp", raw: "ftp://user:" + secret + "@proxy.example.com", wantErr: "scheme"},
		{name: "no scheme", raw: "user:" + secret + "@proxy.example.com:1080", wantErr: "scheme"},
		{name: "host and port only", raw: "proxy.example.com:1080", wantErr: "scheme"},
		{name: "empty host", raw: "socks5://:1080", wantErr: "host"},
		{name: "credentials without host", raw: "socks5://user:" + secret + "@", wantErr: "host"},
		{name: "opaque form", raw: "socks5:proxy.example.com:1080", wantErr: "host"},
		{name: "path", raw: "http://proxy.example.com:3128/" + secret, wantErr: "only scheme"},
		{name: "query", raw: "http://proxy.example.com:3128?key=" + secret, wantErr: "only scheme"},
		{name: "bad escape in password", raw: "socks5://user:%zz" + secret + "@proxy.example.com", wantErr: "not a valid URL"},
		{name: "space in password", raw: "socks5://user:" + secret + " x@proxy.example.com", wantErr: "not a valid URL"},
		{name: "garbage", raw: "://" + secret, wantErr: "not a valid URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseProxy(tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "proxy.example.com") {
				t.Errorf("error quotes the URL: %v", err)
			}
		})
	}
}

func TestNewClientRejectsBadProxy(t *testing.T) {
	_, err := NewClient("ftp://user:"+secret+"@proxy.example.com", time.Second)
	if err == nil || !strings.HasPrefix(err.Error(), "proxy: ") {
		t.Fatalf("error = %v, want it to start with the parameter name", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the password: %v", err)
	}
}

func TestNewClientWithoutProxyKeepsEnvironment(t *testing.T) {
	client, err := NewClient("", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if client.Timeout != 5*time.Second {
		t.Errorf("timeout = %s", client.Timeout)
	}
	if client.Transport.(*http.Transport).Proxy == nil {
		t.Error("without a proxy URL the environment proxy must stay in effect")
	}
}

func TestNewClientProxyOverridesEnvironment(t *testing.T) {
	client, err := NewClient("socks5://203.0.113.5:1080", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// Loopback destinations are exempt from the environment proxy but not
	// from an explicit one.
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	got, err := client.Transport.(*http.Transport).Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Host != "203.0.113.5:1080" {
		t.Errorf("proxy = %v", got)
	}
}

// backend answers every request with its own address, so a test can tell who
// was reached.
func backend(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "backend "+r.Host)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func get(t *testing.T, client *http.Client, target string) (string, error) {
	t.Helper()

	resp, err := client.Get(target)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)

	return string(body), err
}

func TestClientThroughSOCKS5(t *testing.T) {
	api := backend(t)

	tests := []struct {
		name   string
		scheme string
		user   string
		pass   string
	}{
		{name: "socks5", scheme: "socks5"},
		{name: "socks5h", scheme: "socks5h"},
		{name: "login and password", scheme: "socks5", user: "user", pass: "p@ss:word"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := socks5test.Start(t, tt.user, tt.pass)
			// The host name does not resolve anywhere: only the proxy knows
			// where it leads, as with a real API behind a remote proxy.
			proxy.Route("api.example.test:80", api.Listener.Addr().String())

			proxyURL := url.URL{Scheme: tt.scheme, Host: proxy.Addr}
			if tt.user != "" {
				proxyURL.User = url.UserPassword(tt.user, tt.pass)
			}

			client, err := NewClient(proxyURL.String(), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}

			body, err := get(t, client, "http://api.example.test/")
			if err != nil {
				t.Fatal(err)
			}
			if body != "backend api.example.test" {
				t.Errorf("body = %q", body)
			}
			if got := proxy.Targets(); len(got) != 1 || got[0] != "api.example.test:80" {
				t.Errorf("proxy targets = %v, want the host name passed on for the proxy to resolve", got)
			}
		})
	}
}

func TestClientThroughHTTPProxy(t *testing.T) {
	api := backend(t)

	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())

		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.URL.Host = api.Listener.Addr().String()

		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)

	client, err := NewClient(proxy.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := get(t, client, "http://api.example.test/zone"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "http://api.example.test/zone" {
		t.Errorf("proxy saw %v, want the request addressed to the API host", seen)
	}
}

func TestClientProxyUnreachable(t *testing.T) {
	// A port that was just closed refuses connections.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	for _, scheme := range []string{"socks5", "http"} {
		t.Run(scheme, func(t *testing.T) {
			client, err := NewClient(scheme+"://user:"+secret+"@"+addr, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}

			_, err = get(t, client, "http://api.example.test/")
			if err == nil || !strings.Contains(err.Error(), "proxyconnect") {
				t.Fatalf("error = %v, want it to name the proxy connection", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error leaks the password: %v", err)
			}
		})
	}
}

func TestClientProxyRejectsCredentials(t *testing.T) {
	proxy := socks5test.Start(t, "user", "right")

	client, err := NewClient("socks5://user:"+secret+"@"+proxy.Addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	_, err = get(t, client, "http://api.example.test/")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the password: %v", err)
	}
	if got := proxy.Targets(); len(got) != 0 {
		t.Errorf("proxy served %v despite the wrong password", got)
	}
}

func TestNewClientDirectIgnoresProxy(t *testing.T) {
	for _, value := range []string{"direct", "Direct", " DIRECT "} {
		client, err := NewClient(value, time.Second)
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}

		if client.Transport.(*http.Transport).Proxy != nil {
			t.Errorf("%q: the transport still has a proxy function, so the environment would apply", value)
		}
		if !IsDirect(value) {
			t.Errorf("IsDirect(%q) = false", value)
		}
	}

	for _, value := range []string{"", "socks5://203.0.113.5:1080", "directly"} {
		if IsDirect(value) {
			t.Errorf("IsDirect(%q) = true", value)
		}
	}
}

func TestDirectIsNotAProxyURL(t *testing.T) {
	_, err := ParseProxy("direct")
	if err == nil || !strings.Contains(err.Error(), "direct") {
		t.Errorf("error = %v, want it to point at the direct keyword", err)
	}
}
