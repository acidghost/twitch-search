# Twitch Search Utility
Lists live followed channels and lists VoDs.

## Install
Usual Go tools: `go build .`.

## Usage
Use the `-live` flag to list followed live channels.

List VoDs of channel given by `-vod`.

## FzF Integration
```bash
# List VoDs and start selection with streamlink
twitch_search_vods() {
  twitch-search -vod="$1" \
    | fzf --ansi --height=50% --layout=reverse \
    | awk '{print $1}' \
    | xargs -I{} -o bash -c 'nohup streamlink --player-passthrough hls {} best >/dev/null & echo {}'
}

# List live channels and start streamlink and chatterino on the selection
twitch_search_live() {
  twitch-search -live \
    | fzf --ansi --height=50% --layout=reverse \
    | awk '{print $1}' \
    | xargs -I{} -o bash -c 'nohup streamlink --player-passthrough hls https://twitch.tv/{} best >/dev/null & \
                             nohup chatterino -c t:{} >/dev/null & echo {}'
}
```
