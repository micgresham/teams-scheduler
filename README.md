# Teams Status Scheduler

A desktop app for macOS and Windows that sets your Microsoft Teams status
from schedules you define, and can keep the computer awake. It's written in
Go and ships as native compiled binaries.

**No Microsoft Entra ID (Azure AD) app registration is needed.** The app
changes your status by operating the Teams status menu, the same way you
would: profile picture → *"Available, change status"* → *Busy*. It uses the
operating system's accessibility interface to do this.

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
- **Two ways to reach Teams:**
  - **The new Teams desktop app.** It can be minimised, hidden or have its
    window closed. The app brings it up in the background without stealing
    focus, then puts it back as it was.
  - **Teams web in a browser** (Chrome, Edge, Safari, Firefox, Arc, Brave, …).
    Keep teams.microsoft.com open as the active tab of a browser window.
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
3. Allow **Accessibility** access. Go to System Settings → Privacy &
   Security → Accessibility and switch on **Teams Status Scheduler**. The
   app prompts for this, and the status panel shows *Grant access…* until
   it's allowed.

   Things that can get in the way:
   - **You're not an administrator on the Mac.** Switching on an app under
     Accessibility needs an administrator's name and password. On a work
     Mac, ask IT to allow **Teams Status Scheduler**, or have them approve
     it for you.
   - **You rebuild the app.** Each unsigned rebuild counts as a new app to
     macOS. Remove the old entry with the "–" button and allow the new one.
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
internal/engine/         timer loop, apply-on-change, retry back-off, reset on exit
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

## Limitations

- **Driving the UI is slower than an API.** A status change takes about 5
  seconds. The app acts only when the scheduled status changes, and **Apply
  now** re-applies it. If you change your status by hand in Teams, the app
  won't override it until the next scheduled change.
- **Teams can still show activity-based states.** Examples are "In a call"
  and "Presenting".
- **macOS needs the Accessibility permission, which needs an
  administrator** to switch on. On a Mac where you're not an admin and IT
  won't allow it, the app can't change your Teams status. Sleep prevention
  and the scheduling still work. The app also can't use Microsoft Graph,
  because that needs an Entra ID app registration.
- **Windows builds are compiled but untested** on real hardware. The macOS
  build has been tested end to end against new Teams (2026).
- **Schedules use the computer's local time zone.** A block from 00:00 to
  00:00 covers the full day.
