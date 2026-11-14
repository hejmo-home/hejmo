package pki

import "crypto"

// CAKeyForTest exposes the CA key so tests can prove its limits.
func CAKeyForTest(s *Store) crypto.Signer { return s.caKey }
