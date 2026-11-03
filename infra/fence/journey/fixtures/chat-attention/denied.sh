# A native prompt holds working before the real permission; Escape interrupts the turn.
_attention_denied_begin() {
  MAIN_STOP_HOLD="$ARTIFACT/denied-prepermission.hold"
  printf '%s\n' held >"$MAIN_STOP_HOLD" || return 2
  _attention_begin denied "$1" && _attention_wait working || return "$?"
  rm "$MAIN_STOP_HOLD" || return 2
  _attention_wait blocked
}
