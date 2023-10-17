package main

import "crypto/tls"

func getTlsConfig(getCertFunc func(hello *tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	conf := &tls.Config{
		MinVersion:       tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		},
		GetCertificate: getCertFunc,
	}
	if *http3Enabled {
		conf.NextProtos = []string{"h3", "h2", "http/1.1"}
	} else {
		conf.NextProtos = []string{"h2", "http/1.1"}
	}
	return conf

}
