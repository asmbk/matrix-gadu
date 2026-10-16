# matrix-gadu

A Matrix puppeting bridge for Gadu-Gadu (GG).

The bridge connects your Matrix account to your GG account. Each GG contact appears as a Matrix user (`@gg_<number>:your.server`), and each GG chat appears as a Matrix room.

The bridge uses [mautrix-go bridgev2](https://github.com/mautrix/go/tree/main/bridgev2) for the Matrix side and [libgadu](https://github.com/wojtekka/libgadu) for the GG protocol.

> **Status: early development (0.1.0-dev).** The bridge runs against the real GG servers and is in daily use on one server since October 2026. Expect bugs and changes. See [SPEC.md](SPEC.md) for the plan and the open decisions.

## Features

| Feature | State |
|---|---|
| One-to-one chats, both directions | Done |
| Multi-line messages as one message | Done |
| Bold, italic and underline | Done |
| Contact names from the GG contact list | Done |
| Start a chat by GG number or by a DM to a ghost | Done |
| A room for every contact on the GG list (`contact_rooms`) | Done |
| Contact search by name or number (`search` command) | Done |
| Messages received while offline | Done |
| Typing notifications, both directions | Done |
| Delivery receipts | Done |
| Messages from your other GG clients (multilogon) | Done, not yet tested against GG |
| Reconnect with backoff, detection of a conflicting GG client | Done |
| Online status of contacts as Matrix presence (shown by FluffyChat, Element) | Done |
| Status in the room topic, for clients without presence such as NeoChat (`status_in_topic`) | Done |
| Status emoji in the room name, visible in every room list (`status_in_name`) | Done |
| `online` command: list online contacts, or the status of one contact | Done |
| Contact avatars from GG (public GG avatar service) | Done |
| Edit the GG contact list: add, remove and rename contacts (other entries, groups and phone numbers stay) | Done |
| Block and unblock contacts ("Ignorowani" group, sent to the GG server after each login) | Done |
| Rooms of offline contacts in the low priority section (`offline_low_priority`) | Done |
| Your own picture for a contact room (kept, not sent to GG) | Done |
| Images from GG (GG Drive images and Giphy GIFs become Matrix images; other GG Drive files stay links) | Done |
| Images from older GG clients such as Kadu (GG image protocol, up to 255 KiB) | Done |
| Images to older GG clients such as Kadu (made smaller to fit 255 KiB, a link under the image) | Done |
| Images and files to GG (sent as public links, needs `public_media` in the config and a reverse proxy route) | Done |
| GG conferences, history import | Not planned for 0.1 |

GG has no edits, reactions or replies. The bridge rejects edits and reactions and sends a reply as a normal message.

## Requirements

- A Matrix homeserver that supports application services (Synapse, Tuwunel, Continuwuity and others).
- Docker, or a Linux build environment with Go 1.26 or later, a C compiler, `pkg-config` and `libgadu-dev`.

## Set up with Docker

1. Build the image:

   ```sh
   docker build -t matrix-gadu .
   ```

2. Create a data directory that the container user (UID 1337) can write:

   ```sh
   mkdir data && sudo chown 1337:1337 data
   ```

3. Start the container once. It writes `data/config.yaml` and stops.

   ```sh
   docker run --rm -v "$PWD/data:/data" matrix-gadu
   ```

4. Edit `data/config.yaml`. Set at least these fields:

   - `homeserver.address` and `homeserver.domain`
   - `appservice.address` (the URL where the homeserver reaches the bridge)
   - `database`: for SQLite, set `type: sqlite3-fk-wal` and `uri: file:/data/gadu.db?_txlock=immediate`
   - `bridge.permissions`: for example `"example.org": user` and `"@you:example.org": admin`

5. Start the container again. It writes `data/registration.yaml` and stops.

6. Add `registration.yaml` to the homeserver as an application service and restart the homeserver. See the [mautrix docs](https://docs.mau.fi/bridges/general/registering-appservices.html).

7. Start the bridge:

   ```sh
   docker run -d --name matrix-gadu --restart unless-stopped -v "$PWD/data:/data" -p 127.0.0.1:29360:29360 matrix-gadu
   ```

8. Open a chat with `@ggbot:example.org` and send `login`. Enter your GG number and password.

## Build from source

```sh
sudo apt install golang libgadu-dev pkg-config gcc
go build -tags goolm -o matrix-gadu ./cmd/matrix-gadu
```

The `goolm` tag selects the pure-Go encryption library, so you do not need `libolm`.

### Build without root access

If you cannot install `libgadu-dev`, unpack the Debian or Ubuntu packages into `.deps/root` and use `scripts/dev-env.sh`:

```sh
mkdir -p .deps/debs && cd .deps/debs
apt-get download libgadu-dev libgadu3t64
for f in *.deb; do dpkg-deb -x "$f" ../root; done
cd ../..
. scripts/dev-env.sh
go build -tags goolm ./...
go test -tags goolm ./...
```

## Bot commands

The bridge has the standard bridgev2 commands (`help`, `login`, `logout`, `list-logins`, `start-chat`, `resolve-identifier` and others) and these GG commands:

| Command | Effect |
|---|---|
| `reconnect` | Close the GG connection and open it again |
| `sync-contacts` | Load the GG contact list again and update contact names |
| `status [text]` | Set the GG status text. Without text, clear it. |
| `online [name or number]` | List online contacts. With a name or number, show the status of matching contacts, offline ones too. |
| `contact-add <number> [name]` | Add a GG number to the GG contact list. With `contact_rooms`, the bridge creates its room. |
| `contact-remove <number or name>` | Remove a contact from the GG contact list. The Matrix room stays. |
| `contact-rename <number> <new name>` | Change the name of a contact in the GG contact list. The ghost and the room name follow. |
| `block <number or name>` | Block a contact: the bridge puts it in the "Ignorowani" group and GG drops its messages. A number outside the list is added first. |
| `unblock <number or name>` | Unblock a contact. |

To start a chat, send `start-chat <GG number>` to the bot, or open a DM with `@gg_<number>:example.org`. To find a contact's number, send `search <part of the name>`.

## Configuration

The `network` section of `config.yaml` is specific to this bridge:

| Option | Default | Meaning |
|---|---|---|
| `displayname_template` | `{{or .ShowName .Nickname .FullName .UIN}}` | Name of a contact ghost. Fields: `.UIN`, `.ShowName`, `.Nickname`, `.FirstName`, `.LastName`, `.FullName`. |
| `initial_status` | `available` | Your GG status while the bridge runs: `available`, `ffc`, `busy`, `dnd`, `invisible`. |
| `multilogon` | `true` | Ask the GG servers to keep your other GG sessions online. |
| `tls` | `required` | `required` rejects a bad server certificate. `enabled` accepts it and is less safe. |
| `max_message_length` | `1900` | The bridge splits longer messages. A split message loses its formatting. |
| `status_in_topic` | `false` | Write the contact's status into the room topic, for example "🟡 Busy – at work". Use it with clients that do not show Matrix presence, such as NeoChat. Each change is a state event, and some clients show a line for it. |
| `status_in_name` | `false` | Put a status emoji in front of the room name, for example "🟢 Kuba", so that the status is visible in the room list of every client. Each change is a state event. |
| `image_links` | `false` | Show any link that points to an image as the image itself (GG Drive images and Giphy GIFs are always shown). The bridge connects only to public IP addresses, but the owner of a linked server sees the IP address of the bridge. |
| `offline_low_priority` | `false` | Move the rooms of offline contacts to the low priority section of the room list, so that online contacts stay at the top. Needs double puppeting. The bridge never changes a room that you tagged as favourite or low priority yourself. |
| `status_changes_read` | `false` | Mark the room name and topic changes of a status change as read, so that NeoChat does not show the room as unread. The bridge does it only when you read everything else in the room. Needs double puppeting. |
| `contact_rooms` | `true` | Create a room for every contact on the GG contact list, so that all contacts are visible in the client. The bridge checks the list after each login and adds rooms for new contacts. On a server with many users, set it to `false`: each user gets a room for every contact. |

All other sections are standard bridgev2 sections, with one changed default: the example config sets `encryption.allow: true`, so the bot also works in encrypted DMs, which most clients create by default. `encryption.default` stays `false`, so contact rooms are unencrypted (see the NeoChat note under Troubleshooting).

## Images and files

Today's GG clients send images as links: GG Drive (`www.gg.pl/dysk/…`) and Giphy. The bridge downloads GG Drive images and Giphy GIFs and posts them as Matrix images. Other GG Drive files stay as links, because GG allows their download only in a browser. With `image_links: true`, the bridge also shows any other link that points to an image. It connects only to public IP addresses, never to the local machine or private networks.

GG has no way for third-party clients to upload files, so the bridge sends your images and files to GG as links. Turn on `public_media` in the config, set `appservice.public_address` to the public address of the bridge, and route `/_mautrix/publicmedia/*` from your reverse proxy to the bridge. Anyone with such a link can open the file.

## Security

- GG has no login tokens. The bridge stores your GG password in its database. Protect the database file and its backups.
- `logout` deletes the login and the stored password.
- The bridge does not log message text or passwords.
- GG does not allow unofficial clients in its terms of use. Use the bridge at your own risk.

## Troubleshooting

- **The bridge and the GG phone app disconnect each other.** The GG servers allow several sessions only with multilogon. Keep `multilogon: true`. If the bridge reports `gg-conflict`, close the other client.
- **Login fails with a TLS error.** Try `tls: enabled`. The bridge then accepts a bad server certificate.
- **NeoChat shows contact messages without text.** NeoChat's encryption library (libQuotient) rejects messages in encrypted bridge rooms, because the bridge bot owns the encryption key and the contact sends the message (`Sender from event does not match sender from session`). FluffyChat and Element accept them. If you use NeoChat, keep `encryption.default: false`, so that the bridge creates unencrypted contact rooms. `encryption.allow: true` still lets encrypted chats with the bot work.
- **The bridge crashed.** A bug in libgadu can stop the process. Use a restart policy (`--restart unless-stopped`) and report the log.

## Test the GG layer by hand

`cmd/gadu-spike` logs in to GG, prints the contact list and all events, and can send one test message. It does not need Matrix.

```sh
go build -tags goolm -o gadu-spike ./cmd/gadu-spike
GG_UIN=123456 ./gadu-spike -to 654321 -msg "test" -wait 2m
```

The program asks for the password, or reads it from `GG_PASSWORD`. Do not run it on a number that another client without multilogon uses at the same time. The two sessions disconnect each other.

## Project layout

```
cmd/matrix-gadu     bridge main program
cmd/gadu-spike      manual test program for the GG layer
pkg/gadu            Go API for GG (no Matrix code) and the contact list parser
pkg/gadu/libgadu    libgadu implementation (the only cgo code)
pkg/gadu/fake       in-memory implementation for tests
pkg/connector       bridgev2 network connector
```

## License

The bridge is licensed under the GNU Affero General Public License, version 3 or later (AGPL-3.0-or-later). See [LICENSE](LICENSE). It uses mautrix-go (MPL-2.0) and links to libgadu (LGPL-2.1).
