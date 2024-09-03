#!/bin/sh

# Set the GIN_MODE to release
export GIN_MODE=release

# Run the application
exec "$@"