package server

import (
	"fmt"

	"github.com/jdelic/artifactsd/internal/config"
)

// listenAndServe starts the HTTP listener according to the TLS mode (design/06
// §6). ACME/autocert is wired in artifactsd-4dd.
func (s *Server) listenAndServe() error {
	switch s.cfg.TLS.Mode {
	case config.TLSOff:
		return s.http.ListenAndServe()
	case config.TLSFiles:
		return s.http.ListenAndServeTLS(s.cfg.TLS.CertFile, s.cfg.TLS.KeyFile)
	case config.TLSACME:
		return s.listenAndServeACME()
	default:
		return fmt.Errorf("server: unknown tls mode %q", s.cfg.TLS.Mode)
	}
}
