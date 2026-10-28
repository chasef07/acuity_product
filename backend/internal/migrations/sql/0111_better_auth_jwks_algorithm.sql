SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE auth.jwks ADD COLUMN "alg" text, ADD COLUMN "crv" text;
