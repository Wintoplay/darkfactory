#!/bin/sh
# The daemon's "shell" preset runs $SHELL -l. The probe points $SHELL here so
# every session it creates is the deterministic TUI fixture, not the operator's
# shell. -l is accepted and ignored.
exec "${DF_FIXTURE_TUI:?DF_FIXTURE_TUI is not set}" "$@"
