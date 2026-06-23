package server

import (
	"crypto/tls"
	"os"

	"golang.org/x/crypto/acme/autocert"
)

// acmeManager builds the autocert manager for tls=acme (decision D4): automatic
// Let's Encrypt certificates restricted to the configured domains, cached on
// disk under acme-cache (default <data>/acme).
func (s *Server) acmeManager() *autocert.Manager {
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(s.cfg.TLS.ACMEDomains...),
		Cache:      autocert.DirCache(s.cfg.TLS.ACMECacheDir),
		Email:      s.cfg.TLS.ACMEEmail,
	}
}

// listenAndServeACME serves HTTPS with automatically-provisioned certificates.
// The autocert TLSConfig answers the TLS-ALPN-01 challenge on the TLS port, so
// no separate HTTP listener is required.
func (s *Server) listenAndServeACME() error {
	if err := os.MkdirAll(s.cfg.TLS.ACMECacheDir, 0o700); err != nil {
		return err
	}
	m := s.acmeManager()
	tlsConf := m.TLSConfig()
	tlsConf.MinVersion = tls.VersionTLS12
	s.http.TLSConfig = tlsConf
	return s.http.ListenAndServeTLS("", "")
}
