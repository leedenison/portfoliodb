#!/bin/sh
# Create the login role Grafana reads the telemetry schema with.
#
# The privileges live on telemetry_reader, a NOLOGIN group role, and are granted
# by server/migrations/005_telemetry.sql where they can be reviewed in the
# repository. Only the login and its password come from here, which is what keeps
# a password out of a file in the repository.
#
# The ordering is the subtle part. This runs from /docker-entrypoint-initdb.d,
# which fires on an empty data directory, before the service has ever connected
# and so before the migration has run. telemetry_reader therefore does not exist
# yet and this script has to create it; the migration guards its own CREATE ROLE
# on pg_roles, finds this one and only adds the grants. Do not "fix" the apparent
# duplication by deleting either half. No guard is needed here because the
# cluster is empty by definition, and the test and e2e stacks, which mount
# nothing, take the role from the migration alone.
#
# Note that initdb only runs on an empty data directory while the dev stack keeps
# postgres_data across make stop / make run, so an existing stack needs
# `make clean-docker` once before this script has ever fired.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
	-v reader="$TELEMETRY_READER_USER" -v password="$TELEMETRY_READER_PASSWORD" <<'SQL'
CREATE ROLE telemetry_reader NOLOGIN;
CREATE ROLE :"reader" LOGIN PASSWORD :'password';
GRANT telemetry_reader TO :"reader";
SQL
