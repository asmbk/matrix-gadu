# matrix-gadu — Specification

| | |
|---|---|
| Status | Draft 3 (implementation started) |
| Date | 2026-10-11 |
| Owner | Michal Bok |
| Target | Any Matrix homeserver that supports application services |

## 1. Summary

`matrix-gadu` is a Matrix puppeting bridge for the Gadu-Gadu (GG) instant messenger.
It connects a Matrix account to a GG account.
Each GG contact appears as a Matrix user, and each GG conversation appears as a Matrix room.

The bridge uses two existing building blocks:

- **mautrix-go bridgev2** for everything on the Matrix side: appservice, rooms, ghosts, double puppeting, encryption and the database.
- **libgadu** (C, through cgo) for the GG protocol (GG 11, protocol version `0x40`).

The bridge is a general-purpose, open-source project.
Any homeserver administrator can run it for the users of that homeserver.

## 2. Background

### 2.1 The problem

Today, the usual way to reach GG from Matrix is a chain of programs, for example:

```
Matrix client ─▶ homeserver ─▶ IRC bridge (Heisenbridge) ─▶ IRC ─▶ BitlBee ─▶ libpurple (prpl-gg) ─▶ libgadu ─▶ GG
```

The IRC layer in this chain causes these problems:

- Contact names are IRC nicks, for example `Jakub_Kowalski`.
- GG messages with several lines arrive as separate messages.
- A new chat starts only with an IRC command such as `QUERY <nick>`. Normal Matrix DMs do not work when the IRC bridge cannot read encrypted rooms.
- The chain shows no online status per contact, no typing and no delivery confirmation.
- Another GG client on the same number (for example the GG phone app) kicks the bridge session.
- Four separate programs must be installed, configured and updated.

### 2.2 Alternatives that were rejected

| Alternative | Reason |
|---|---|
| matrix-bifrost with node-purple | Tested on 2026-10-11. node-purple crashed with a segfault on start and on GG login. |
| Bifrost xmpp.js + Prosody + Spectrum 2 | Four components in a chain. Bifrost xmpp.js does not puppet a user's own account. |
| matrix-appservice-irc + BitlBee | Keeps all IRC limits. |
| libpurple through cgo | Large C layer with global state. Hides GG-only features (acks, multilogon). libpurple 3 dropped GG. |
| Pure-Go GG protocol | Possible later. Too much work for the first version. See section 6.6. |

### 2.3 State of libgadu

The libgadu authors archived the project at the end of 2025.
Release 1.12.2 is the last release. Debian and other distributions still ship it.
The project keeps its own fork of libgadu for fixes (section 11.2).

## 3. Goals and non-goals

### 3.1 Goals

1. Bridge one-to-one GG chats in both directions, with whole multi-line messages.
2. Show GG contacts with their real names from the GG contact list.
3. Let users start a chat from any Matrix client with a normal DM to a contact ghost.
4. Show online status and status text, typing and delivery confirmation.
5. Show messages that the user sends from other GG clients (multilogon), if the GG servers support it.
6. Run as one Docker container next to any homeserver, with the standard mautrix setup steps.
7. Support several Matrix users on one bridge, each with their own GG account.
8. Keep a clean Go interface around libgadu, so that a pure-Go implementation can replace it later.

### 3.2 Non-goals for version 1

- Chat history import or backfill. GG has no server-side archive, and imports from local client logs come later, if at all.
- GG group conferences (`gg_chat_*`).
- File transfers (DCC) and voice calls.
- GG account registration and password change.
- GG public directory search.
- Install scripts, update scripts or packages for specific platforms. The project ships a Docker image and a binary.
- A hosted public service (section 13.2).

## 4. Use cases

| ID | Use case |
|---|---|
| UC-1 | A user receives a GG message and reads it in any Matrix client, with a push notification. |
| UC-2 | The user replies from any Matrix client. The contact receives one GG message. |
| UC-3 | The user starts a new chat with a contact from the Matrix client, by GG number or from the contact list. |
| UC-4 | The user sees who is online and reads status texts. |
| UC-5 | The user writes from the GG phone app. The message also appears in Matrix as the user's own message. |
| UC-6 | The bridge loses the GG connection. It reconnects and reports the state in the management room. |
| UC-7 | A homeserver administrator installs the bridge with the Docker image and the generated config and registration. |

## 5. Architecture

```
┌──────────────┐   Client-Server API   ┌────────────┐  appservice API  ┌──────────────────────────────┐
│ Matrix       │ ◀──────────────────▶ │ homeserver │ ◀──────────────▶ │ matrix-gadu                  │
│ clients      │                       └────────────┘                   │  ├─ bridgev2 (mautrix-go)    │
└──────────────┘                                                        │  ├─ pkg/connector            │
                                                                        │  └─ pkg/gadu  (Go API)       │
                                                                        │      └─ cgo ─▶ libgadu (C)   │──TLS──▶ GG servers
                                                                        └──────────────────────────────┘
```

### 5.1 Repository layout

The layout follows the mautrix bridgev2 bridges (for example `mautrix/bluesky`).

```
matrix-gadu/
├── cmd/matrix-gadu/main.go          # mxmain.BridgeMain bootstrap
├── cmd/gadu-spike/main.go           # manual test program for the GG layer
├── pkg/gadu/                        # Go API for GG, no Matrix code
│   ├── gadu.go  status.go           #   Session interface, types
│   ├── contactlist.go               #   GG 10 (XML) and GG 7 contact list parser
│   ├── libgadu/                     #   libgadu implementation (cgo)
│   └── fake/                        #   in-memory implementation for tests
├── pkg/connector/                   # bridgev2 NetworkConnector + NetworkAPI
│   ├── connector.go  config.go  login.go  client.go
│   ├── handlegg.go   handlematrix.go  chatinfo.go  ids.go
│   ├── capabilities.go  dbmeta.go  msgconv.go  commands.go
│   └── example-config.yaml
├── third_party/libgadu/             # git submodule: project fork of libgadu (phase 3)
├── scripts/dev-env.sh               # build against libgadu unpacked into .deps/
├── Dockerfile  docker-run.sh
├── SPEC.md
├── README.md
└── LICENSE                          # AGPL-3.0-or-later
```

### 5.2 Rules for the packages

- `pkg/gadu` has no Matrix imports. `pkg/connector` never calls libgadu directly.
- Only `pkg/gadu/libgadu` contains cgo code. `cmd/matrix-gadu` connects the two: it sets `GaduConnector.NewSession` to `libgadu.New`.
- `pkg/gadu/fake` implements the same interface, for unit tests of the connector.

## 6. GG layer (`pkg/gadu`)

### 6.1 Go interface

```go
type Session interface {
    Connect(ctx context.Context) error          // blocks until login succeeds or fails
    Close() error
    Events() <-chan Event                       // closed when the session ends
    SendMessage(ctx context.Context, to UIN, html string) (Seq, error)
    SendTyping(ctx context.Context, to UIN, length int) error
    SetStatus(ctx context.Context, s Status, descr string) error
    Notify(ctx context.Context, contacts []UIN) error   // subscribe to status
    RequestContactList(ctx context.Context) error       // result arrives as an event
}
```

A `Session` is used once. After a disconnect, the connector creates a new `Session`.
`Connect` returns a `*LoginError` with a reason when the login fails.
libgadu makes the plain text part of a message from the HTML part, so `SendMessage` takes only HTML.

`Event` is a sum type with these variants:

| Go event | libgadu event | Meaning |
|---|---|---|
| (return value of `Connect`) | `GG_EVENT_CONN_SUCCESS`, `GG_EVENT_CONN_FAILED` | Login result, with a reason on failure (bad password, network, TLS) |
| `DisconnectedEvent` | `GG_EVENT_DISCONNECT` | The server closed the session. Another client can cause this. |
| `MessageEvent` | `GG_EVENT_MSG` | Incoming message: sender, time, HTML, plain text |
| `OwnMessageEvent` | `GG_EVENT_MULTILOGON_MSG` | A message that the user sent from another GG session |
| `AckEvent` | `GG_EVENT_ACK110` / `GG_EVENT_ACK` | `ACK110`: the server accepted the message. `ACK`: delivery state. |
| `StatusEvent` | `GG_EVENT_NOTIFY60`, `GG_EVENT_STATUS60` | Contact status and status text |
| `TypingEvent` | `GG_EVENT_TYPING_NOTIFICATION` | Contact is typing |
| `ContactListEvent` | `GG_EVENT_USERLIST100_REPLY` | Server-side contact list |
| `MultilogonInfoEvent` | `GG_EVENT_MULTILOGON_INFO` | List of other active sessions |

Images (`GG_EVENT_IMAGE_REQUEST`, `GG_EVENT_IMAGE_REPLY`) come after version 1.

### 6.2 Login parameters

| Field of `struct gg_login_params` | Value |
|---|---|
| `uin`, `password` | From the user login |
| `protocol_version` | `GG_PROTOCOL_VERSION_110` |
| `encoding` | `GG_ENCODING_UTF8` |
| `tls` | `GG_SSL_REQUIRED` |
| `protocol_features` | `GG_FEATURE_ALL \| GG_FEATURE_MULTILOGON` |
| `status` | `GG_STATUS_AVAIL` (or the configured status) |
| `async` | `1` |
| `resolver` | `GG_RESOLVER_PTHREAD` (never `GG_RESOLVER_FORK` in a Go process) |

`GG_FEATURE_ALL` does not include `GG_FEATURE_MULTILOGON`. The bridge must add it explicitly.

### 6.3 Threading model

libgadu is not thread-safe. The cgo implementation uses this model:

1. One goroutine owns each `struct gg_session`. It calls `runtime.LockOSThread()`.
2. The goroutine waits with `poll()` on `gs->fd`. It uses `gs->check` for the read and write flags and `gs->timeout` (seconds) for the timeout.
3. When the socket is ready, the goroutine calls `gg_watch_fd()`. When the timeout expires, it first sets `gs->timeout = 0`, because libgadu reads a zero timeout as "the time for this state ran out". It converts the result to a Go event and calls `gg_event_free()`.
4. Other goroutines send commands through a channel. The owner goroutine wakes up through a self-pipe and runs the command.
5. The owner goroutine sends `gg_ping()` every 60 seconds.
6. After `GG_EVENT_CONN_SUCCESS`, the session sends an empty contact list (`gg_notify(gs, NULL, 0)`). The server delivers stored offline messages only after it receives a contact list. The connector adds the contacts later with `gg_add_notify()`.
7. `Close()` calls `gg_logoff()` and `gg_free_session()` on the owner goroutine.

The implementation copies all C strings to Go memory before it frees the event.

### 6.4 Reconnect

The connector (`pkg/connector/client.go`) owns the reconnect logic, because a `Session` is used once.

- After a disconnect, the connector reconnects with exponential backoff: 5 s, 10 s, 30 s, 60 s, then 5 min.
- After a failed login with a bad password, the session does not retry. The connector reports `BAD_CREDENTIALS`.
- If the server disconnects the session more than 3 times in 10 minutes, the connector reports a conflict with another GG client and waits 15 minutes.

### 6.5 Message format

| Direction | Rule |
|---|---|
| GG → Matrix | Use the plain part as `body`. Sanitize the HTML part with an allowlist (`b`, `i`, `u`, `br`, `a` with http, https or mailto). Drop colors and fonts, because every GG message carries them. Add `formatted_body` only if formatting remains. |
| Matrix → GG | Convert `formatted_body` to the GG HTML subset (`b`, `i`, `u`, `br`), wrapped in the `<span style=...>` that libgadu and the GG clients use. Write links as "text (url)". Drop reply fallbacks. Send with `gg_send_message_html()`. |
| Replies, edits, reactions | GG has no such features. A reply goes as a normal message. The bridge rejects edits and reactions through the room capabilities. |
| Media | Version 1 sends a text note instead of the file. |
| Length | The bridge splits messages longer than the GG limit (about 2,000 characters) on line or word boundaries. |

### 6.6 Way to a pure-Go implementation

The `Session` interface hides libgadu completely.
A future `pkg/gadu/native` package can implement the same interface in Go.
The connector does not change when the implementation changes.

## 7. Matrix layer (`pkg/connector`)

### 7.1 Identifiers

| Item | Value | Example |
|---|---|---|
| `networkid.UserID` | GG number as text | `1234567` |
| Ghost Matrix ID | `@gg_<uin>:<domain>` (configurable template) | `@gg_1234567:example.org` |
| Bot Matrix ID | `@ggbot:<domain>` (configurable) | |
| `networkid.UserLoginID` | GG number of the user | `7654321` |
| `networkid.PortalID` (DM) | GG number of the contact | `1234567` |
| `networkid.PortalKey.Receiver` | `UserLoginID` | DM portals are per login |
| `networkid.MessageID` | `<uin>:<time ns>:<hash>` for incoming, `own:<uin>:<time ns>:<hash>` for multilogon, `out:<seq>:<time ns>` for outgoing | Sequence numbers repeat across sessions |

### 7.2 Login flow

One flow, `password`, of type user input:

1. The user sends `login` to the bridge bot.
2. The bot asks for the GG number and the password.
3. The connector connects once. If the login succeeds, it stores the number and the password in `UserLoginMetadata`.

The bridge must keep the password, because GG has no login tokens.
See section 12.1 for the protection of the password.

### 7.3 Interfaces

| bridgev2 interface | Phase | Purpose |
|---|---|---|
| `NetworkConnector`, `NetworkAPI` | 1 | Required |
| `IdentifierResolvingNetworkAPI` | 1 | Start a chat by GG number |
| `GhostDMCreatingNetworkAPI` | 1 | DM to a ghost creates a portal |
| `ContactListingNetworkAPI` | 2 | Contact list for "start chat" in clients |
| `TypingHandlingNetworkAPI` | 2 | Matrix typing → GG typing |
| `UserSearchingNetworkAPI` | later | Search in the contact list |

### 7.4 Ghosts

- The display name comes from the GG contact list: nickname, else "first name last name", else the GG number.
- The connector loads the contact list after each login and updates the ghosts.
- Avatars come after version 1, if a public GG avatar URL still works.

### 7.5 Portals

- Version 1 supports only DM portals.
- The bridge creates a portal on the first incoming message, or on a DM from the user to a ghost.
- The bridge does not create portals for all contacts at login.
- Encryption follows the standard bridgev2 `encryption` config section.

### 7.6 Status and presence (phase 2)

- The connector sends `Notify()` for all contacts after login.
- Contact status changes become Matrix presence of the ghost: available → `online`, away or busy → `unavailable`, offline → `offline`. The status text becomes `status_msg`.
- bridgev2 has no presence interface, and its `MatrixAPI` has no presence call. The connector must call the Matrix presence API directly as the ghost. Homeservers that ignore presence for appservice users show no status. **Not implemented yet.**
- The user's own GG status follows `network.initial_status` while the bridge runs. The `status` command sets a status text.

### 7.7 Delivery state (phase 2)

- An outgoing message gets a GG sequence number.
- `AckEvent` with a delivered state becomes a success message status (MSS) event.
- A missing ack after 30 seconds keeps the message as sent, without an error.

### 7.8 Multilogon (phase 2)

- `OwnMessageEvent` becomes a message from the user in the portal, sent through double puppeting.
- The bridge does not echo these messages back to GG.
- Phase 0 checks whether the GG servers still support multilogon. If not, this feature is removed.

### 7.9 Bot commands

The bridge keeps the standard bridgev2 commands (`login`, `logout`, `list-logins`, `help` and others) and adds:

| Command | Phase | Effect |
|---|---|---|
| `reconnect` | 1 | Close and open the GG session |
| `sync-contacts` | 2 | Load the GG contact list again and update names |
| `status [text]` | 2 | Set or clear the GG status text |

## 8. Configuration

The bridge uses the standard mautrix config file, generated with `matrix-gadu -e`.
Only the `network` section is specific to this bridge:

```yaml
network:
    # Ghost display name template. Fields: .Nickname .FirstName .LastName .UIN
    displayname_template: "{{or .Nickname (printf \"%s %s\" .FirstName .LastName) .UIN}}"
    # Status at login: available, ffc, busy, dnd, invisible
    initial_status: available
    # Use multilogon (other GG sessions of the user stay online)
    multilogon: true
    # TLS: required or enabled (enabled accepts bad certificates)
    tls: required
    # Split outgoing messages longer than this
    max_message_length: 1900
```

All other sections (homeserver, appservice, database, bridge permissions, double puppeting, encryption, logging) are standard bridgev2 sections.
The README shows a minimal example for a homeserver at `example.org`.

## 9. Deployment

### 9.1 Artifacts

| Artifact | Content |
|---|---|
| Docker image | `ghcr.io/<owner>/matrix-gadu:<version>` and `:latest`, for `linux/amd64` and `linux/arm64` |
| Binary | Release builds for `linux/amd64` and `linux/arm64`, dynamically linked against the bundled `libgadu.so` |
| Source | Build with Go and the libgadu submodule |

### 9.2 Container image

A multi-stage `Dockerfile`:

1. Build the Go binary with cgo and the `goolm` build tag (pure-Go encryption, no `libolm`).
2. Copy the binary into a small Debian runtime image with the `libgadu3t64` package.

Until the libgadu fork exists (phase 3), the image uses the Debian `libgadu` package (1.12.2). Phase 3 changes step 1 to build libgadu from `third_party/libgadu` (needs GnuTLS, protobuf-c, zlib).

The image runs as a non-root user and uses `/data` as the volume for config, registration and database.
It follows the mautrix convention: on the first start without a config, it writes an example config and exits.

### 9.3 Setup steps (README)

1. Start the container once. It writes `/data/config.yaml`.
2. Edit the config: homeserver address and domain, database, permissions.
3. Start the container again. It writes `/data/registration.yaml`.
4. Add the registration to the homeserver as an application service. Restart the homeserver.
5. Start the bridge.
6. Send `login` to the bridge bot.

### 9.4 Supported homeservers

- Synapse, Tuwunel and Continuwuity are the test targets.
- Any homeserver that supports application services can work.

## 10. Versions and releases

- Version numbers use semantic versioning, starting at `0.1.0`.
- Each release has a git tag, a changelog entry, a Docker image and binaries.
- The `main` branch always builds. Releases come from tags.

## 11. Maintenance

### 11.1 Dependencies

| Dependency | Rule |
|---|---|
| mautrix-go | Pin the version in `go.mod`. Update every 3 months, or for security fixes. |
| libgadu | Project fork, pinned by commit in the submodule |
| Go toolchain | Pin in `go.mod` (`toolchain`) |
| Base image | Pin by digest |

### 11.2 libgadu fork

- The fork starts at tag `1.12.2` of `wojtekka/libgadu`.
- It carries only needed fixes: build fixes, TLS fixes, and protocol changes from the GG servers.
- Each fix has a test, if possible.

## 12. Security and privacy

### 12.1 GG password

- The bridge stores the password in its database, because GG has no tokens.
- The README tells administrators to protect the database file (file permissions, encrypted backups).
- The bridge never logs the password.
- `logout` deletes the stored password.

### 12.2 Transport

- GG connections use TLS (`GG_SSL_REQUIRED`) with the hub host allowlist from libgadu.
- If the GG servers need weaker TLS settings, the config option `tls: enabled` exists. The bridge logs a warning when it is on.
- The example config binds the appservice listener to `127.0.0.1`.

### 12.3 Logs

- Logs never contain message text, passwords or tokens.
- Logs can contain GG numbers, event types and error codes.

### 12.4 Access

- Access uses the standard bridgev2 permissions (`block`, `relay`, `commands`, `user`, `admin`).
- The example config allows only users of the local homeserver.

## 13. Distribution

### 13.1 Project

- Public repo with the AGPL-3.0-or-later license.
- Docker image on `ghcr.io` and release binaries.
- README with requirements, setup steps, config reference, limits and troubleshooting.
- CI on every push: build, unit tests, `go vet`, and a lint step.

### 13.2 Out of scope

- A hosted bridge for users of other homeservers. It needs federation, storage of other people's GG passwords, and GDPR obligations. Other people can run it at their own risk.
- Install helpers (for example an Ansible role or an all-in-one compose file). These can come later from the community or from the project.

## 14. Testing

| Level | Content |
|---|---|
| Unit | HTML conversion both ways, message splitting, ID functions |
| Connector | Connector tests with `pkg/gadu/fake`: login, incoming message, outgoing message, reconnect |
| cgo | `cmd/gadu-spike` against a real GG test account, run by hand |
| End to end | Manual checklist (section 14.1) with at least two Matrix clients |

### 14.1 Manual checklist

1. Login with a correct and with a wrong password.
2. Incoming message with one line and with several lines.
3. Outgoing message with several lines and with formatting.
4. Start a chat by GG number from a Matrix client.
5. Network loss and reconnect.
6. Logout deletes the stored password.
7. Phase 2: contact goes online, away and offline. Status text changes.
8. Phase 2: typing in both directions.
9. Phase 2: delivery confirmation.
10. Phase 2: message from the GG phone app (multilogon).

## 15. Phases and acceptance criteria

| Phase | Content | Done when |
|---|---|---|
| **0. Spike** | `cmd/gadu-spike`: cgo login, contact list, send and receive one message, check multilogon | Login over TLS works. One message goes each way. The multilogon result is known. |
| **1. MVP** | Connector, login flow, DM portals, text both ways, ghost names, offline messages, double puppeting, Docker image, README | Checklist items 1–6 pass. |
| **2. Comfort** | Contact list in clients, status and presence, typing, acks, multilogon, `sync-contacts`, `status` | Checklist items 7–10 pass. |
| **3. Release** | libgadu fork as submodule, CI, multi-arch image, binaries, `0.1.0` tag | A new user can set up the bridge from the README alone. |

### 15.1 Implementation state (2026-10-11)

| Phase | State |
|---|---|
| 0 | `cmd/gadu-spike` and `pkg/gadu/libgadu` are written and compile. **Not yet run against the GG servers.** |
| 1 | Connector, login, DM portals, text both ways, ghost names, offline messages, `reconnect`, Docker image and README are done. Unit and connector tests pass (with `-race`). Manual checklist not run. |
| 2 | Done: contact list for clients, typing both ways, delivery receipts, multilogon messages, `sync-contacts`, `status`. Not done: presence (section 7.6). |
| 3 | Not started: libgadu fork, release builds, multi-arch image. CI workflow exists. |

## 16. Risks

| Risk | Effect | Action |
|---|---|---|
| GG changes the protocol | Bridge stops working | Patch the libgadu fork. Keep the pure-Go path open (section 6.6). |
| GG servers need old TLS | Login fails with `tls: required` | Config option `tls: enabled`, with a warning |
| Multilogon no longer works | Phone app and bridge kick each other | Remove the feature. Document "one GG client at a time". |
| A libgadu bug crashes the process | Bridge restarts | Restart policy in the README, crash log, fix in the fork |
| mautrix-go API changes | Build breaks on update | Pin the version. Update on purpose. |
| GG terms of use forbid unofficial clients | Account block, in theory | README warns users. Low request rate. |
| GG shuts down | Bridge has no purpose | Archive the project |

## 17. Open decisions

| # | Decision | Proposal | State |
|---|---|---|---|
| D1 | Project name | `matrix-gadu` | **decided** |
| D2 | GG backend | libgadu through cgo | **decided** |
| D3 | History | Not in version 1 | **decided** |
| D4 | Scope | General-purpose public project, no platform-specific scripts | **decided** |
| D5 | Test GG number | A second GG account | open |
| D6 | GitHub account or organization for the repo | Personal account `asmbk`, module `github.com/asmbk/matrix-gadu` | decided |
| D7 | Repo visibility | Public from the start, or after phase 1 | open |
| D8 | License | AGPL-3.0-or-later | decided |
| D9 | Default database | SQLite (PostgreSQL supported through bridgev2) | proposed |
| D10 | Portal creation | On demand only | proposed |

## 18. References

- mautrix-go bridgev2: <https://github.com/mautrix/go/tree/main/bridgev2>
- Example bridge: <https://github.com/mautrix/bluesky>
- libgadu (archived): <https://github.com/wojtekka/libgadu>, docs: <https://libgadu.github.io/>
- libgadu API header: `include/libgadu.h.in` in the libgadu repo
