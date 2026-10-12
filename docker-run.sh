#!/bin/sh
# Start script of the container. It follows the mautrix convention:
# the first start writes config.yaml, the second writes registration.yaml.
set -e
cd /data
if [ ! -f config.yaml ]; then
	/usr/bin/matrix-gadu -c config.yaml -e
	echo "Wrote /data/config.yaml. Edit it, then start the container again."
	exit 0
fi
if [ ! -f registration.yaml ]; then
	/usr/bin/matrix-gadu -c config.yaml -g -r registration.yaml
	echo "Wrote /data/registration.yaml. Add it to the homeserver, then start the container again."
	exit 0
fi
exec /usr/bin/matrix-gadu -c config.yaml -r registration.yaml
