\set ON_ERROR_STOP on

REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA auth FROM PUBLIC;

GRANT USAGE ON SCHEMA public
TO acuity_portal, acuity_provider, acuity_realtime, acuity_worker;
GRANT USAGE ON SCHEMA auth TO acuity_auth;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public
TO acuity_portal, acuity_realtime, acuity_worker;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public
TO acuity_portal, acuity_realtime, acuity_worker;

REVOKE ALL ON TABLE public.schema_migrations
FROM acuity_portal, acuity_realtime, acuity_worker;
GRANT INSERT, SELECT, UPDATE
ON TABLE public.human_calling_provider_receipts
TO acuity_provider;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA auth
TO acuity_auth;
GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA auth
TO acuity_auth;

ALTER DEFAULT PRIVILEGES FOR ROLE acuity_migrate IN SCHEMA public
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES
TO acuity_portal, acuity_realtime, acuity_worker;
ALTER DEFAULT PRIVILEGES FOR ROLE acuity_migrate IN SCHEMA public
GRANT USAGE, SELECT, UPDATE ON SEQUENCES
TO acuity_portal, acuity_realtime, acuity_worker;
ALTER DEFAULT PRIVILEGES FOR ROLE acuity_migrate IN SCHEMA auth
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES
TO acuity_auth;
ALTER DEFAULT PRIVILEGES FOR ROLE acuity_migrate IN SCHEMA auth
GRANT USAGE, SELECT, UPDATE ON SEQUENCES
TO acuity_auth;
