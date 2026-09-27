# Teams Status Scheduler

A desktop app for macOS and Windows that sets your Microsoft Teams status
from schedules you define, and can keep the computer awake. It's written in
Go and ships as native compiled binaries.

There are three ways it can change your status:

- **Teams desktop app** and **Teams web in a browser**: no Microsoft
  Entra ID (Azure AD) app registration is needed. The app operates the
  Teams status menu the same way you would: profile picture →
  *"Available, change status"* → *Busy*. It uses the operating system's
  accessibility interface to do this.
- **Microsoft Graph**: uses Microsoft's official presence API through an
  Entra ID app registration. There's no clicking and no Accessibility
  permission, but someone must create the registration (see
  [Using Microsoft Graph](#using-microsoft-graph-entra-id-app-registration)).

This is **fair use ware**: free to use. If you find it useful, see
[Fair use ware](#fair-use-ware).

## Features

- **Multiple schedules at once, with priorities.** Priority **1 is the
  highest**; when schedules overlap, the lowest number wins. Example: a
  priority-10 "Work week", a priority-5 "Stand-up" (Busy) and a priority-1
  "Focus mornings" (Do not disturb) stacked on top of each other.
- **Time blocks** on chosen weekdays. A block can run overnight (for
  example 17:00–09:00). **Optional date ranges** cover things like
  vacations.
- **Weekly timeline** that shows the final result after priorities are
  applied.
- **Three ways to reach Teams:**
  - **The new Teams desktop app.** It can be minimised, hidden or have its
    window closed. The app brings it up in the background without stealing
    focus, then puts it back as it was.
  - **Teams web in a browser** (Chrome, Edge, Safari, Firefox, Arc, Brave, …).
    Keep teams.microsoft.com open as the active tab of a browser window.
  - **Microsoft Graph**, with your own Entra ID app registration.
- **Prevent sleep:** *Off*, *While a schedule is active*, or *Always, while
  the app is running*. This keeps SSH and other remote sessions from
  dropping. It is independent of the Teams side: your status keeps
  following the schedule in every mode, and pausing schedules doesn't
  release the sleep block.
  - macOS uses IOKit power assertions (the same mechanism as
    `caffeinate -i -s`).
  - Windows uses `SetThreadExecutionState`.
  - **A MacBook still sleeps when you close the lid**, unless it's on power
    with an external display (clamshell mode).
- **Tray / menu-bar icon.** Closing the window keeps the app running. From
  the icon you can pause/resume, apply now and change sleep prevention.
- **Launch at login** (optional). On macOS this is a LaunchAgent; on Windows
  it's the HKCU Run key.
- **Back to automatic.** When no schedule applies, when you pause, or when
  you quit (configurable), the app chooses **Reset status**, so Teams goes
  back to setting your presence automatically.

## Install and first run

| Platform | File |
|---|---|
| Mac with Apple silicon (M1–M4) | `TeamsStatusScheduler-<ver>-macos-arm64.zip` |
| Intel Mac | `TeamsStatusScheduler-<ver>-macos-x86_64.zip` |
| Windows 10/11 (Intel/AMD) | `TeamsStatusScheduler-<ver>-windows-x64.exe` |
| Windows 11 on ARM | `TeamsStatusScheduler-<ver>-windows-arm64.exe` |

**macOS**

1. Unzip, then drag **Teams Status Scheduler.app** to Applications.
2. The app isn't notarized, so the first time you open it, right-click →
   **Open** → **Open**.
3. Allow **Accessibility** access. The Microsoft Graph method doesn't need
   this. Go to System Settings → Privacy &
   Security → Accessibility and switch on **Teams Status Scheduler**. The
   app prompts for this, and the status panel shows *Grant access…* until
   it's allowed.

   Things that can get in the way:
   - **You're not an administrator on the Mac.** Switching on an app under
     Accessibility needs an administrator's name and password. On a work
     Mac, ask IT to allow **Teams Status Scheduler**, or have them approve
     it for you.
   - **You update or rebuild the app.** The app is ad-hoc signed, so macOS
     treats each new build as a different app. The old "Teams Status
     Scheduler" entry can still look switched on while the new build is
     refused. Select the entry, remove it with the **–** button, and click
     **Grant access…** again (or add the app with **+**). Quit and reopen
     the app if the status doesn't update.
   - **Your Mac is managed by your company (MDM).** IT policy may block
     Accessibility access for apps it hasn't approved.

**Windows**

1. Run the `.exe`. It's a single file, and no install is needed. Windows
   needs **no special permission or admin rights**: UI Automation is
   available to every app.
2. SmartScreen may warn about an unsigned app: click **More info → Run
   anyway**.
3. The Microsoft Edge WebView2 Runtime is required. Windows 10/11 already
   include it.

Then set up a schedule (Add schedule) and pick how to reach Teams (Settings →
*Change status using*).

## Using Microsoft Graph (Entra ID app registration)

With this method the app calls Microsoft Graph's presence API as you. This
is the same "preferred presence" you set by picking a status in Teams.

### What you need

- A **work or school** Microsoft 365 account. The presence API doesn't
  support personal Microsoft accounts.
- An **app registration** in your organisation's Microsoft Entra ID.
  Creating one needs the right role. By default any member can create one,
  but many organisations switch that off, and then only an administrator or
  an *Application Developer* can.
- **Consent** for two delegated permissions: `Presence.ReadWrite` and
  `User.Read`. Neither normally needs an admin's approval. You approve them
  yourself at first sign-in, unless your organisation has turned off user
  consent. In that case an administrator must click **Grant admin
  consent**.

### Create the registration

1. Sign in to https://entra.microsoft.com and go to **Identity →
   Applications → App registrations → New registration**.
   - **Name:** `Teams Status Scheduler` (any name works).
   - **Supported account types:** *Accounts in this organizational
     directory only* (single tenant).
   - **Redirect URI:** choose **Public client/native (mobile & desktop)** and
     enter `http://localhost`.
2. On the app's **Overview** page, copy the **Application (client) ID** and
   the **Directory (tenant) ID**.
3. On **Authentication**, set **Allow public client flows** to **Yes** and
   click Save. No client secret or certificate is needed: this is a
   desktop app, and it signs in with PKCE.
4. On **API permissions**:
   - Choose **Add a permission → Microsoft Graph → Delegated permissions**.
     Add **Presence.ReadWrite** (`User.Read` is usually there already).
   - If your organisation requires it, click **Grant admin consent for
     <your org>**. That button needs an administrator.

### Connect the app

1. Open **Settings → Change status using → Microsoft Graph (Entra ID app
   registration)**.
2. Paste the **Application (client) ID**. Under **Tenant**, enter the
   **Directory (tenant) ID** or your domain (e.g. `contoso.com`).
3. Click **Save and sign in…**. Your browser opens the normal Microsoft
   sign-in page, including MFA. The first time, you're asked to approve the
   permissions. Once you're signed in, the browser shows a "you can close
   this window" message and the app shows *Signed in as …*.

### How it behaves

- **Statuses expire on their own.** Each status is sent with an expiry at
  the end of its time block (plus two minutes). If the app stops running,
  your status goes back to automatic instead of sticking. The app re-sends
  the current status every 30 minutes, which keeps long blocks alive and
  undoes a manual change within half an hour.
- **Pausing, ending a schedule, and signing out** clear the preferred
  presence, which hands control back to Teams.
- **Sign-in is stored securely.** The app keeps its tokens in an encrypted
  file in the data folder, so you normally sign in once. On macOS the
  encryption key is in your Keychain; on Windows the file is protected with
  DPAPI. Tokens are renewed automatically. If your organisation forces
  re-authentication, the status panel shows **Sign in…**.

### Common sign-in errors

| Error | Fix |
|---|---|
| *AADSTS65001 / "Need admin approval"* | An administrator must click **Grant admin consent** on the API permissions page. |
| *AADSTS7000218* | Turn on **Allow public client flows** (step 3). |
| *AADSTS50011* (redirect URI mismatch) | Add `http://localhost` under **Mobile and desktop applications** (step 1). |
| *AADSTS700016* (app not found) | Check the client ID, and that the tenant in Settings is the one the app is registered in. |
| Graph 403 when setting status | `Presence.ReadWrite` is missing, or consent wasn't granted. |

## If a status change fails

Teams occasionally changes its menus. If that breaks the status change, or
if your Teams isn't in English:

1. Go to **Settings → Inspect Teams UI…**. This opens your profile and
   status menus and lists every control name the app can see. Your status
   isn't changed. The list is also saved to `teams-ui-inspection.txt` in the
   data folder.
2. Go to **Settings → Edit UI labels…**. This opens `ui_labels.json`: the
   case-insensitive regular expressions used to find:
   - `profileButton`: the avatar, e.g. *"Your profile, status Available"*
   - `statusOpener`: e.g. *"Available, change status"*
   - `statusItems`: the six statuses
   - `resetItem`: *"Reset status"*
3. Adjust the patterns to match the inspection output. The changes apply on
   the next status change, with no rebuild. To go back to the built-in
   defaults, delete the file.

When a change fails, the app retries with a growing delay (1, 2, 4 … up to
15 minutes). This stops it from opening Teams menus every minute. **Apply
now** retries straight away.

## Building

```bash
./makebin.sh                          # all four targets → dist/
./makebin.sh macos-arm64 windows-x64  # just some
SKIP_TESTS=1 ./makebin.sh             # skip the test run
```

All four targets build **on a Mac**. That needs:

- **Go 1.25+.**
- **The Xcode command-line tools** (`xcode-select --install`). Apple's
  clang builds both Mac architectures.
- **Nothing extra for the Windows builds.** They're pure Go with no C
  compiler, so they cross-compile. Their icon, version info and manifest are
  embedded with `go-winres`, which `go run` fetches automatically.

Output:

```
dist/TeamsStatusScheduler-1.0.0-macos-arm64.zip     # .app bundle, ad-hoc signed
dist/TeamsStatusScheduler-1.0.0-macos-x86_64.zip
dist/TeamsStatusScheduler-1.0.0-windows-x64.exe     # single file, GUI subsystem
dist/TeamsStatusScheduler-1.0.0-windows-arm64.exe
```

The version comes from `VERSION`.

**Signing for wider distribution:**

- **macOS:** `CODESIGN_ID="Developer ID Application: …" ./makebin.sh`,
  then notarize the zip with `xcrun notarytool`. A stable Developer ID
  signature also means the Accessibility permission survives app updates.
- **Windows:** sign the `.exe` with `signtool` to avoid SmartScreen
  warnings.

## Development

```bash
go run .                 # run from source (macOS: grant Accessibility to your terminal/IDE)
go test ./...            # unit tests (schedules, click recipe, engine, API)

# Live test against the installed Teams desktop app on a Mac.
# It CHANGES your status through several values, then resets it.
go test -tags realteams -run TestRealTeams -v ./internal/teams/

# Serve the UI with demo data at http://127.0.0.1:8765 for browser testing.
TSS_SERVE_UI=127.0.0.1:8765 go test -run TestServeUI .
```

### Layout

```
main.go                  app setup: window, tray menu, single instance, signals
server.go                local JSON API used by the UI (served only to the app's webview)
frontend/                UI (plain HTML/CSS/JS, embedded in the binary)
internal/schedule/       schedule model; which status wins now / next change
internal/engine/         timer loop, apply-on-change, retry back-off, reset on exit;
                         Router picks UI automation or Graph per the chosen method
internal/graph/          Microsoft Graph: MSAL sign-in, presence calls, encrypted token cache
internal/teams/          click recipe + drivers
  recipe.go              profile → "…, change status" → status; Inspect; label overrides
  driver_darwin.go       macOS Accessibility driver (+ ax_darwin.m, cgo)
  driver_windows.go      Windows UI Automation driver (COM via go-ole, pure Go)
internal/keepawake/      sleep prevention (IOKit / SetThreadExecutionState)
internal/autostart/      launch at login (LaunchAgent / Run key)
assets/                  icons
makebin.sh               builds all four targets
```

### Data

| | macOS | Windows |
|---|---|---|
| Settings and schedules | `~/Library/Application Support/TeamsStatusScheduler/config.json` | `%AppData%\TeamsStatusScheduler\config.json` |
| Log | same folder, `teams-status.log` | same folder |
| UI label overrides | same folder, `ui_labels.json` | same folder |
| Graph sign-in tokens | same folder, `graph-token-cache.bin` (AES-GCM; key in Keychain) | same folder (DPAPI) |

## Limitations

- **Driving the UI is slower than an API.** With the desktop and browser
  methods a status change takes about 5 seconds. The app acts only when the scheduled status changes, and **Apply
  now** re-applies it. If you change your status by hand in Teams, the app
  won't override it until the next scheduled change. (With Microsoft Graph,
  the change is instant and re-sent every 30 minutes.)
- **Teams can still show activity-based states.** Examples are "In a call"
  and "Presenting".
- **macOS needs the Accessibility permission, which needs an
  administrator** to switch on. On a Mac where you're not an admin and IT
  won't allow it, the desktop and browser methods can't change your Teams
  status. Use the Microsoft Graph method instead, if you can get an app
  registration. Sleep prevention and the scheduling work either way.
- **The Graph method hasn't been tested against a real tenant yet.** Its
  requests and settings are unit-tested, and sign-in uses Microsoft's own
  MSAL library.
- **Windows builds are compiled but untested** on real hardware. The macOS
  build has been tested end to end against new Teams (2026).
- **Schedules use the computer's local time zone.** A block from 00:00 to
  00:00 covers the full day.

## Fair use ware

Teams Status Scheduler is **fair use ware**: you're free to use it. If you
find it useful, you can
[buy me a coffee](https://buymeacoffee.com/micgresham). ☕
