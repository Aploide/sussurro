#!/bin/bash
# Trigger script for Sussurro on Wayland
# Bind this script to your keyboard shortcut in your DE settings.
#
# Usage: trigger.sh [command]
#
#   toggle    start recording, or stop one already running (default)
#   press     begin recording; in review mode, begin an edit instruction
#   release   end the recording started by press
#   edit-start begin an edit only when reviewed text is ready
#   edit-stop  end the recording started by edit-start
#   cancel    abandon the session and discard any held text
#   deliver   insert the reviewed text
#   submit    insert the reviewed text and press Enter
#
# toggle is the default, so existing bindings keep working unchanged.
# Compositors that can bind key press and release separately should bind
# press and release for push-to-talk. edit-start, edit-stop, cancel, deliver,
# and submit require review mode (workflow.mode: review in the config).

set -u

SOCKET="${XDG_RUNTIME_DIR:-/tmp}/sussurro.sock"
COMMAND="${1:-toggle}"

if [ ! -S "$SOCKET" ]; then
	notify-send "Sussurro" "Sussurro is not running" 2>/dev/null
	echo "Sussurro is not running (no socket at $SOCKET)" >&2
	exit 1
fi

# The server replies with a status line; surface it so a rejected command is
# visible rather than silently ignored.
if command -v nc >/dev/null 2>&1; then
	reply=$(echo "$COMMAND" | nc -U "$SOCKET" 2>/dev/null)
elif command -v socat >/dev/null 2>&1; then
	reply=$(echo "$COMMAND" | socat - UNIX-CONNECT:"$SOCKET" 2>/dev/null)
else
	echo "Neither nc nor socat is available" >&2
	exit 1
fi

case "$reply" in
ERROR*)
	notify-send "Sussurro" "$reply" 2>/dev/null
	echo "$reply" >&2
	exit 1
	;;
*)
	echo "$reply"
	;;
esac
