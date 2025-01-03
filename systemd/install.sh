#! /bin/sh
set -eu

cd "$(dirname "$0")"

systemd_user_dir=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user
service_file=regular.service

mkdir -p "$systemd_user_dir"
awk -v user="$USER" '{ gsub(/%USER%/, user); print }' <"$service_file" >"$systemd_user_dir"/"$service_file"

systemctl --user daemon-reload
systemctl --user enable "$service_file"
systemctl --user start "$service_file"
