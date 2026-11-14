-- HTTPS with a publicly trusted certificate (ADR 0014): the family's DNS name,
-- DNS provider and certificate authority. At most one row. Credentials are
-- stored sealed (internal/secret), never as plain text.
CREATE TABLE public_https (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    name               TEXT NOT NULL,                 -- DNS name of the box, e.g. familie.dedyn.io
    zone               TEXT NOT NULL,                 -- DNS zone the name lives in
    provider           TEXT NOT NULL,                 -- DNS provider, e.g. desec
    issuer             TEXT NOT NULL CHECK (issuer IN ('letsencrypt', 'actalis')),
    lan_ip             TEXT NOT NULL,                 -- address the name points to
    email              TEXT NOT NULL DEFAULT '',      -- contact for the certificate authority
    token              BLOB NOT NULL,                 -- sealed DNS API token
    eab_key_id         TEXT NOT NULL DEFAULT '',      -- external account binding of the issuer (Actalis)
    eab_hmac           BLOB,                          -- sealed
    renewal_error      TEXT NOT NULL DEFAULT '',      -- why the last renewal failed, empty after a success
    renewal_failed_at  TEXT
);
