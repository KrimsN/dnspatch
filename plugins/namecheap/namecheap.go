// Package namecheap provides a provider for domains using Namecheap's
// Dynamic DNS feature
// (https://www.namecheap.com/support/knowledgebase/article.aspx/29/11/how-do-i-use-a-browser-to-dynamically-update-the-hosts-ip/).
//
// The service is not dyndns2: the password travels in the query string
// rather than as HTTP Basic auth, the response is an XML document instead of
// dyndns2's good/nochg vocabulary, and only A records are supported, so it
// needs its own plugin.
package namecheap

import "github.com/dnspatch/dnspatch/plugin"

// Name is the type name of the provider in the configuration file.
const Name = "namecheap"

func init() {
	plugin.RegisterProvider(Name, func(cfg Config) (plugin.Provider, error) {
		return newProvider(cfg, nil)
	})
}
