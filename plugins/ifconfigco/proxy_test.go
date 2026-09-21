package ifconfigco

import (
	"net/http"
	"testing"
)

// A retriever that went through a proxy would report the address of the proxy
// and the daemon would publish it in DNS, so the environment must not matter.
func TestRetrieverIgnoresProxyEnvironment(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(name, "http://proxy.example.com:3128")
	}

	r, err := newRetriever(Config{BaseURL: "https://ifconfig.co", Family: "ipv4"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	transport, ok := r.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", r.client.Transport)
	}

	// http.ProxyFromEnvironment reads the variables once per process, so
	// checking the outcome for a request would depend on test order; a nil
	// function is the only state that never consults them.
	if transport.Proxy != nil {
		t.Error("the retriever transport has a proxy function; it must always connect directly")
	}
}
