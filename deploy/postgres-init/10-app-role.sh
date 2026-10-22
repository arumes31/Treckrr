#!/bin/sh
# Creates the non-superuser runtime role Treckrr should connect as. The postgres
# image runs this once, on the FIRST start of an empty data volume, as the
# bootstrap superuser (POSTGRES_USER). That superuser stays for administration;
# the app's DATABASE_URL should use APP_DB_USER instead, so a SQL-level bug or a
# crafted restore cannot reach COPY ... FROM PROGRAM, lo_import and friends.
#
# Opt-in: nothing happens unless APP_DB_USER and APP_DB_PASSWORD are both set.
# Existing volumes are not touched; see README ("Database role") for converting
# an existing installation by hand.
#
# The entrypoint SOURCES this file when it is not executable, so it must not call
# `exit` or change shell options.
if [ -n "${APP_DB_USER:-}" ] && [ -n "${APP_DB_PASSWORD:-}" ]; then
	echo "treckrr: creating non-superuser application role ${APP_DB_USER}"
	psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
		-v app_user="$APP_DB_USER" -v app_password="$APP_DB_PASSWORD" -v app_db="$POSTGRES_DB" <<'SQL'
CREATE ROLE :"app_user" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS
	PASSWORD :'app_password';
-- Owning the database and the public schema is all the app needs: migrations
-- create tables there and pg_trgm is a trusted extension a database owner may
-- install. The role gets no rights outside this one database.
ALTER DATABASE :"app_db" OWNER TO :"app_user";
ALTER SCHEMA public OWNER TO :"app_user";
SQL
else
	echo "treckrr: APP_DB_USER/APP_DB_PASSWORD not set; the app will connect as ${POSTGRES_USER} (a superuser)"
fi
