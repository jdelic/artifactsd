package meta

import "github.com/jdelic/artifactsd/internal/auth"

// HashSecrets converts any cleartext credentials in the document's auth entries
// into hashes and clears the cleartext, in place. Bearer tokens become a sha256
// hash; basic passwords become an argon2id hash (design/06 §2). Call this on
// write before persisting.
func (d *Document) HashSecrets() error {
	if d.Access == nil || d.Access.Auth == nil {
		return nil
	}
	entries := *d.Access.Auth
	for i := range entries {
		e := &entries[i]
		if e.Token != "" {
			e.TokenHash = auth.HashToken(e.Token)
			e.Token = ""
		}
		if e.Password != "" {
			h, err := auth.HashPassword(e.Password)
			if err != nil {
				return err
			}
			e.PasswordHash = h
			e.Password = ""
		}
	}
	return nil
}

// Redacted returns a deep copy safe to return from the API: cleartext secrets
// are removed and hashes are replaced by has_token/has_password presence flags
// (design/06 §2 — reads never expose secrets or hashes).
func (d *Document) Redacted() *Document {
	c := d.Clone()
	if c == nil || c.Access == nil || c.Access.Auth == nil {
		return c
	}
	entries := *c.Access.Auth
	for i := range entries {
		e := &entries[i]
		e.HasToken = e.Token != "" || e.TokenHash != ""
		e.HasPassword = e.Password != "" || e.PasswordHash != ""
		e.Token = ""
		e.TokenHash = ""
		e.Password = ""
		e.PasswordHash = ""
	}
	return c
}

// HasCleartextSecrets reports whether any auth entry still carries a cleartext
// token or password (i.e. HashSecrets has not yet run). Useful as a guard
// before persistence.
func (d *Document) HasCleartextSecrets() bool {
	if d.Access == nil || d.Access.Auth == nil {
		return false
	}
	entries := *d.Access.Auth
	for i := range entries {
		if entries[i].Token != "" || entries[i].Password != "" {
			return true
		}
	}
	return false
}
