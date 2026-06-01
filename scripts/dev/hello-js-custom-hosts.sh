#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s add|remove\n' "$0" >&2
}

if [[ $# -ne 1 ]]; then
  usage
  exit 2
fi

action="$1"
case "$action" in
add | remove) ;;
*)
  usage
  exit 2
  ;;
esac

hosts_path="${HOSTS_PATH:-/etc/hosts}"
input_path="$hosts_path"
if [[ ! -e "$input_path" ]]; then
  input_path="/dev/null"
fi

tmp="$(mktemp)"
cleanup() {
  rm -f "$tmp"
}
trap cleanup EXIT

ACTION="$action" perl -0pe '
  my $tag = "prx:hello-js-custom-hosts";
  my $domain = "hello-js.test";

  sub remove_managed_blocks {
    my ($text) = @_;

    $text =~ s{(\n?)\# <\Q$tag\E trailing-newline=([01])>\n127\.0\.0\.1[ \t]+\Q$domain\E[ \t]*\n\# </\Q$tag\E>\n?}{
      $2 eq "1" ? $1 : ""
    }ge;

    return $text;
  }

  $_ = remove_managed_blocks($_);

  if ($ENV{"ACTION"} eq "add") {
    my $had_trailing_newline = ($_ eq "" || /\n\z/) ? 1 : 0;
    my $separator = ($_ eq "" || /\n\z/) ? "" : "\n";
    $_ .= $separator
      . "# <$tag trailing-newline=$had_trailing_newline>\n"
      . "127.0.0.1 $domain\n"
      . "# </$tag>\n";
  }
' "$input_path" >"$tmp"

if [[ -e "$hosts_path" ]] && cmp -s "$hosts_path" "$tmp"; then
  printf 'hosts unchanged\n'
  exit 0
fi

if [[ "$hosts_path" == "/etc/hosts" ]]; then
  printf 'updating %s; sudo may ask for your password\n' "$hosts_path"
fi

if [[ -w "$hosts_path" || ( ! -e "$hosts_path" && -w "$(dirname "$hosts_path")" ) ]]; then
  cp "$tmp" "$hosts_path"
else
  sudo cp "$tmp" "$hosts_path"
fi

printf 'hosts %s complete\n' "$action"
