#!/bin/sh
# A local process in the backend's real PTY; never starts a remote shell.
# Input is echoed only after this process reads it, rather than by the PTY line discipline.
stty -echo
printf 'fake-ssh-ready\n'
while IFS= read -r line; do
  if [ "$line" = "exit-terminal" ]; then exit 7; fi
  printf '%s\n' "$line"
done
