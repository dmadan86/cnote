-- Ties a pending SAML request to the browser that started it: sha256 of a nonce held in that browser's cookie.
ALTER TABLE core.saml_requests ADD COLUMN binding text;
