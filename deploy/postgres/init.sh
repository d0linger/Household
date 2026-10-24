#!/bin/sh
set -eu
HOUSEHOLD_OWNER_PASSWORD="$(cat /run/secrets/owner_password)"
HOUSEHOLD_APP_PASSWORD="$(cat /run/secrets/app_password)"
export HOUSEHOLD_OWNER_PASSWORD HOUSEHOLD_APP_PASSWORD
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -v ON_ERROR_STOP=1 <<'SQL'
\getenv owner_password HOUSEHOLD_OWNER_PASSWORD
\getenv app_password HOUSEHOLD_APP_PASSWORD
SELECT format('CREATE ROLE household_owner LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD %L', :'owner_password') \gexec
SELECT format('CREATE ROLE household_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD %L', :'app_password') \gexec
SELECT format('ALTER DATABASE %I OWNER TO household_owner', current_database()) \gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', current_database()) \gexec
SELECT format('GRANT CONNECT ON DATABASE %I TO household_app,household_owner', current_database()) \gexec
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
SQL
unset HOUSEHOLD_OWNER_PASSWORD HOUSEHOLD_APP_PASSWORD
