# songrequest — song requests for Twitch

**Your viewers request songs right from the stream, and the music plays in your own Spotify.**

A viewer redeems channel points or sends a donation and types a song name.
The app finds the track, queues it and plays it in Spotify. When the queue
runs out, your own music picks up exactly where it stopped. A clean "now
playing" card shows up on your stream.

[**⬇ Download the latest version**](../../releases/latest) · Windows 10/11 · free

**[Русская версия](README.ru.md)**

---

## Who it's for

Twitch streamers who play music through Spotify and want viewers to request
songs — no bots, no websites, no subscriptions.

## What you need

- Windows 10 or 11;
- **Spotify Premium** (Spotify doesn't let apps control playback without it);
- a Twitch channel (Affiliate or Partner for channel points requests);
- OBS, if you want the on-screen card.

## Getting started

1. Download the archive from the [releases page](../../releases/latest) and unzip it.
2. Run `songrequest.exe` — nothing to install.
3. Pick **English** on the first screen. A setup wizard walks you through
   everything step by step: Spotify, Twitch, the channel points reward,
   donations and the OBS card.

After that the app sits quietly in the system tray and updates with one click.

## Features

- **Requests for channel points or donations** (DonationAlerts, DonatePay,
  DonateX). Viewers can type a song name or paste a Spotify, YouTube,
  Yandex Music or VK link.
- **Fair with points.** Track not found or not allowed by your rules — the
  points go back to the viewer automatically.
- **Your music is never lost.** After the requests, Spotify returns exactly to
  where you were, down to the second.
- **"Now playing" card for OBS.** 17 styles with colorways, your own images
  and animations, drag it anywhere in the frame.
- **Not on Spotify? It plays from YouTube.**
- **Chat commands** for you and your mods: queue, skip, volume, pause, bans.
  The bot always answers instead of staying silent.
- **Whole-playlist requests** as a separate reward — played after you or a
  mod approves them.
- **English and Russian** interface.
- **Built-in bypass** for countries where Spotify is blocked: paste your VPN
  key and only Spotify traffic goes through it. Not needed anywhere else —
  the wizard hides it for English users.

## If something doesn't work

Every error in the panel has a short code (like `SP-04`) and a hint on what to
do next. The "Save log and history" button packs everything needed for
troubleshooting into one archive; keys and passwords never go into it.

---

## For developers

```
go run .                      # app window with the panel inside
go run . -в-браузере          # no window, panel in your browser
go run . -без-браузера        # open neither window nor browser
go test ./...                 # tests
.\build.ps1 -Version X.Y.Z    # builds dist\songrequest.exe and the zip
```

Written in Go. The panel is a regular web page embedded in the `.exe`
together with its fonts, so it works offline. The panel lives at
`http://127.0.0.1:8977`, the OBS card at `http://127.0.0.1:8977/widget`.
Settings, database and logs are in `%APPDATA%\songrequest`.

**Bypass** (`internal/tunnel`): takes a VPN key (`vless://`, `trojan://`,
`ss://`, `vmess://`) or a subscription link, downloads `xray.exe`, tries the
servers and keeps one that Spotify answers through. The Windows system proxy
is never touched. The key is stored in Windows Credential Manager and never
ends up in `config.json` or the logs.

**Spotify rate limits** are the main constraint: the current track is read from
the Spotify window title, the position is counted locally, a playing request
is checked every 25 seconds, and any back-off Spotify announces is respected.
