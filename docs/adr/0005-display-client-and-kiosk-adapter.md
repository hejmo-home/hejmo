# 0005. Display client and kiosk adapter

- Status: Accepted
- Date: 14/11/2026

## Context

Target displays are Android E-Ink tablets with touch (Onyx Boox, Bigme). A web page cannot control brightness, screen off or orientation. Options were an open-source kiosk browser, an own Android wrapper app, or a plain web page without hardware control.

Research (14/11/2026) found these maintained open-source kiosk browsers:

| App | License | Remote control | F-Droid |
|---|---|---|---|
| FreeKiosk | MIT | REST API, MQTT | No |
| FullScreendy | MIT | MQTT only | Yes |
| Webview Kiosk | AGPL-3.0 | MQTT; brightness not documented | Yes |

Onyx Boox devices drive the front light through vendor settings that ordinary apps reportedly cannot change on Android 9+. This affects every app, including an own wrapper. Switching the light off may work; smooth dimming probably does not.

## Decision

- The display is a **web page served by the box** that works in any browser.
- Hardware control (sleep mode, front light, keep awake) runs through a **kiosk adapter** interface in the box.
- V1.0 ships one adapter: **FreeKiosk via its REST API**. It is the recommended display app.
- An MQTT adapter (FullScreendy) follows when needed; MQTT arrives with Home Assistant in V3 anyway.
- The acceptance criterion "brightness follows sunset" is weakened to **"front light off at night"**; smooth dimming only where the device allows it.
- An own Android wrapper app remains an option for V2 if both apps fail on the reference tablet.

## Consequences

- The reference tablet must be tested for front-light control before purchase or before package 5.
- Without a kiosk app the display still works; sleep mode then only shows a dark page.
