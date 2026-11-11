package device

import "crypto/tls"

// The TLS "flavor" of a device shapes its ClientHello (ALPN and cipher
// ordering) so that simulated devices don't all share one Go fingerprint.
// This is an approximation: Go's stack can't reproduce a specific firmware's
// JA4 exactly, but it separates an "embedded" device (TLS 1.2, no HTTP/2
// ALPN, a short RSA-friendly cipher list) from a "modern" one (TLS 1.3,
// h2 ALPN). Tune per device type as needed.

func (d *Device) alpn() []string {
	switch d.Profile.TLS {
	case "modern", "":
		return []string{"h2", "http/1.1"}
	case "mqtt":
		return []string{"mqtt"}
	default: // embedded, mbedtls
		return nil
	}
}

func (d *Device) cipherSuites() []uint16 {
	switch d.Profile.TLS {
	case "mbedtls":
		// Small list typical of an mbedTLS build on a constrained device.
		return []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		}
	case "embedded":
		return []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		}
	default:
		return nil
	}
}
